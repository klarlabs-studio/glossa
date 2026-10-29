module go.klarlabs.de/glossa/runtimes/go

go 1.26.5

// In this repository the runtime builds against the messageformat
// beside it. Consumers ignore this replace and resolve the required
// version, so tag messageformat/vX before runtimes/go/vX.
replace go.klarlabs.de/glossa/messageformat => ../../messageformat

require (
	go.klarlabs.de/fortify v1.10.0
	go.klarlabs.de/glossa/messageformat v0.4.1
	golang.org/x/text v0.43.0
)

require (
	github.com/agentable/go-intl v0.2.17 // indirect
	github.com/cockroachdb/apd/v3 v3.2.3 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/kaptinlin/messageformat-go v0.8.6 // indirect
	github.com/kaptinlin/messageformat-go/mf1 v0.8.6 // indirect
)
