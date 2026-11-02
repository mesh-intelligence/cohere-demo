module github.com/petar-djukic/cohere-demo

go 1.26.3

// declarative-agents v0.20261005.0 (f2438998cf01); the shipped UIs take
// @mesh-intelligence/ui-kit published with the same release
//
// The three modules below are one monorepo and are pinned at a single upstream
// commit; `mage deps:pin <tag-or-commit>` moves them together and refuses to
// leave them at mixed commits. Upstream's release tags are repository-root
// tags, which Go does not resolve for nested modules, so the pins are
// pseudo-versions and this comment names the release they came from.
require (
	github.com/magefile/mage v1.17.2
	github.com/mesh-intelligence/declarative-agents/agent-core v0.20260803.1-0.20261005085000-f2438998cf01
	github.com/mesh-intelligence/declarative-agents/applications/catalog v0.20260803.1-0.20261005085000-f2438998cf01
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/mesh-intelligence/declarative-agents/magefiles v0.0.0-20261005085000-f2438998cf01
	go.opentelemetry.io/proto/otlp v1.11.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/dominikbraun/graph v0.23.0 // indirect
	github.com/schlunsen/claude-agent-sdk-go v0.5.1 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/getkin/kin-openapi v0.140.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.22.5 // indirect
	github.com/go-openapi/swag/jsonname v0.25.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/mesh-intelligence/cobbler-scaffold v0.20260612.2-0.20260625213501-22568bff9cdd
	github.com/oasdiff/yaml v0.1.0 // indirect
	github.com/oasdiff/yaml3 v0.0.13 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260720211330-0afa2a65878a // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260720211330-0afa2a65878a // indirect
)

tool golang.org/x/tools/cmd/present
