package telemetry

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PeopleStorageCollector reports how much the kept people chats take
// (decision 0069, Consequences; people-chatting §16): rows and bytes of
// people_messages, people_files and the reports' and held messages'
// copies, from Postgres's catalogue and statistics (no scan), read when
// Prometheus scrapes.
type PeopleStorageCollector struct{ Pool *pgxpool.Pool }

var peopleCollecting sync.Mutex

var (
	peopleRows  = prometheus.NewDesc("taskiem_people_storage_rows", "Rows of the people chats' tables (Postgres's estimate), by table.", []string{"table"}, nil)
	peopleBytes = prometheus.NewDesc("taskiem_people_storage_bytes", "Bytes on disk of the people chats' tables, with indexes and TOAST, by table.", []string{"table"}, nil)
)

func (c PeopleStorageCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- peopleRows
	ch <- peopleBytes
}

func (c PeopleStorageCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, done, ok := collecting(&peopleCollecting)
	if !ok {
		return
	}
	defer done()
	rows, err := PeopleStorage(ctx, c.Pool)
	if err != nil {
		return
	}
	for _, r := range rows {
		ch <- prometheus.MustNewConstMetric(peopleRows, prometheus.GaugeValue, float64(r.Rows), r.Table)
		ch <- prometheus.MustNewConstMetric(peopleBytes, prometheus.GaugeValue, float64(r.Bytes), r.Table)
	}
}

// StorageRow is one table's size: rows (Postgres's estimate, from its
// statistics) and bytes on disk, with its indexes and TOAST.
type StorageRow struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
	Bytes int64  `json:"bytes"`
}

// PeopleStorageTables are the people chats' tables that grow with every kept
// chat (decision 0069, Consequences): the copies' messages and files, and
// the reports' and held messages' copies.
var PeopleStorageTables = []string{"people_messages", "people_files", "people_report_files", "people_held"}

// Querier is a pool or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// PeopleStorage reads the people chats' storage from the catalogue and the
// statistics: no scan, and no row access needed.
func PeopleStorage(ctx context.Context, q Querier) ([]StorageRow, error) {
	rows, err := q.Query(ctx, `SELECT c.relname::text, GREATEST(c.reltuples::bigint, COALESCE(s.n_live_tup, 0), 0), pg_total_relation_size(c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
		LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
		WHERE c.relname = ANY ($1) AND c.relkind = 'r' ORDER BY c.relname`, PeopleStorageTables)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[StorageRow])
}
