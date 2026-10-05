module github.com/israel-duff/taskiem

go 1.26.0

require (
	cel.dev/cel-go v0.32.0
	github.com/evanw/esbuild v0.28.2
	github.com/fastschema/qjs v0.0.6
	github.com/go-chi/chi/v5 v5.3.2
	github.com/google/licensecheck v0.3.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/pressly/goose/v3 v3.28.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	golang.org/x/crypto v0.57.0
	golang.org/x/text v0.42.0
	golang.org/x/time v0.16.0
	sigs.k8s.io/yaml v1.6.0
)

require (
	cel.dev/expr v0.25.1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/tetratelabs/wazero v1.9.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260824195058-e88cd73687aa // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240826202546-f6391c0de4c7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260831171406-18b4a7587f8a // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/fastschema/qjs => ./third_party/qjs
