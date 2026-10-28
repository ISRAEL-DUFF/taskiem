package riders_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	conn "github.com/israel-duff/taskiem/connectors/riders"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/riders"
)

type ops struct {
	tenant uuid.UUID
	filter riders.Filter
	added  riders.In
	exists bool
}

var musa = riders.Rider{ID: uuid.MustParse("01900000-0000-7000-8000-000000000001"), Name: "Musa", Phone: "+2348031234567", Zones: []string{"Lekki"},
	FeeKobo: 150000, Active: true, Reached: "whatsapp", Presence: riders.Presence{State: "free"}}

func (o *ops) List(_ context.Context, tenant uuid.UUID, f riders.Filter) ([]riders.Rider, error) {
	o.tenant, o.filter = tenant, f
	return []riders.Rider{musa}, nil
}
func (o *ops) Get(_ context.Context, _, id uuid.UUID) (riders.Rider, error) {
	if id != musa.ID {
		return riders.Rider{}, riders.ErrNoRider
	}
	return musa, nil
}
func (o *ops) Add(_ context.Context, _ uuid.UUID, in riders.In, _ string) (riders.Rider, error) {
	o.added = in
	if o.exists {
		return musa, riders.ErrExists
	}
	return musa, nil
}
func (o *ops) Retire(context.Context, uuid.UUID, uuid.UUID, string) (riders.Rider, error) {
	now := time.Now()
	x := musa
	x.RetiredAt, x.Active = &now, false
	return x, nil
}

// The actions act for the run's tenant, read riders as workflows read
// them, and add a phone already riding as that rider.
func TestRiderActions(t *testing.T) {
	o := &ops{}
	c := conn.New(o)
	tenant := uuid.New()
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: tenant, Run: uuid.New(), Step: "riders"})
	res, err := c.Actions["list_riders"].Execute(ctx, connector.Request{Input: map[string]any{"zone": "Lekki"}})
	if err != nil {
		t.Fatal(err)
	}
	if o.tenant != tenant || o.filter.Zone != "Lekki" || !o.filter.ActiveOnly || out(res)["count"] != 1 {
		t.Fatalf("list: %+v %v", o, res.Output)
	}
	one := out(res)["riders"].([]any)[0].(map[string]any)
	if one["fee"] != "₦1,500" || one["state"] != "free" || one["reached"] != "whatsapp" || one["retired"] != false {
		t.Errorf("rider %v", one)
	}
	in := map[string]any{"name": "Musa", "phone": "0803 123 4567", "zones": []any{"Lekki"}, "fee_kobo": float64(150000)}
	res, err = c.Actions["add_rider"].Execute(ctx, connector.Request{Input: in})
	if err != nil || out(res)["already"] != false || *o.added.FeeKobo != 150000 || len(*o.added.Zones) != 1 {
		t.Fatalf("add: %v %v %+v", res.Output, err, o.added)
	}
	o.exists = true
	if res, err = c.Actions["add_rider"].Execute(ctx, connector.Request{Input: in}); err != nil || out(res)["already"] != true {
		t.Fatalf("add again: %v %v", res.Output, err)
	}
	if res, err = c.Actions["retire_rider"].Execute(ctx, connector.Request{Input: map[string]any{"id": musa.ID.String()}}); err != nil || out(res)["retired"] != true {
		t.Fatalf("retire: %v %v", res.Output, err)
	}
	if _, err = c.Actions["get_rider"].Execute(ctx, connector.Request{Input: map[string]any{"id": uuid.NewString()}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("an unknown rider: %v", err)
	}
	// Outside a run, nothing.
	if _, err := c.Actions["list_riders"].Execute(context.Background(), connector.Request{}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("outside a run: %v", err)
	}
}

func out(r connector.Response) map[string]any { m, _ := r.Output.(map[string]any); return m }
