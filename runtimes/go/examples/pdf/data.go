package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	glossa "go.klarlabs.de/glossa/runtimes/go"
)

// The documents' data, as in runtimes/go/testdata/documents/data. Every
// amount is a glossa.Decimal: it decodes from a JSON string and formats
// to exactly its digits.

type taxSummary struct {
	Year          string
	Taxpayer      string
	FilingStatus  string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Due           time.Time
	Issued        time.Time
	Lines         []taxLine
	TotalReceipts int
	TotalEUR      glossa.Decimal
	TotalJPY      glossa.Decimal
}

type taxLine struct {
	Item     string // message ID
	Receipts int
	EUR      glossa.Decimal
	JPY      glossa.Decimal
}

type trainingExport struct {
	Athlete       string
	From          time.Time
	To            time.Time
	ExportedAt    time.Time
	Rows          []exportRow
	TotalSessions int
}

type exportRow struct {
	Date       time.Time
	Sessions   int
	Reps       int
	Completion glossa.Decimal
	Distance   glossa.Decimal // kilometers
	Load       glossa.Decimal // kilograms
}

func loadData(docs string) (*taxSummary, *trainingExport, error) {
	tax, export := &taxSummary{}, &trainingExport{}
	for name, v := range map[string]any{"tax-summary": tax, "export": export} {
		b, err := os.ReadFile(filepath.Join(docs, "data", name+".json"))
		if err != nil {
			return nil, nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return nil, nil, fmt.Errorf("%s.json: %w", name, err)
		}
	}
	return tax, export, nil
}
