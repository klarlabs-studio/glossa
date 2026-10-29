module go.klarlabs.de/glossa/runtimes/go/examples/pdf

go 1.26.5

replace (
	go.klarlabs.de/glossa/messageformat => ../../../../messageformat
	go.klarlabs.de/glossa/runtimes/go => ../..
)

require (
	github.com/go-pdf/fpdf v0.9.0
	go.klarlabs.de/glossa/runtimes/go v0.0.0-00010101000000-000000000000
	golang.org/x/image v0.45.0
)

require (
	github.com/agentable/go-intl v0.2.17 // indirect
	github.com/cockroachdb/apd/v3 v3.2.3 // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/kaptinlin/messageformat-go v0.8.6 // indirect
	github.com/kaptinlin/messageformat-go/mf1 v0.8.6 // indirect
	go.klarlabs.de/fortify v1.10.0 // indirect
	go.klarlabs.de/glossa/messageformat v0.4.1 // indirect
	golang.org/x/text v0.41.0 // indirect
)
