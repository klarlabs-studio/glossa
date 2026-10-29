package capture

import "sort"

// Usage is a current usage of a message, as the Context API reports it:
// the fields the coverage report shows.
type Usage struct {
	Key   string
	File  string
	Line  int
	Route string
}

// Coverage compares the current usages with the captures (RFC 0004 §3.2):
// a message with a current usage but no visible region is not captured,
// so a team can add the routes where it appears.
type Coverage struct {
	// Messages is how many messages have a current usage; Captured how
	// many of them have a visible region.
	Messages    int           `json:"messages"`
	Captured    int           `json:"captured"`
	NotCaptured []NotCaptured `json:"not_captured"`
}

// NotCaptured is a used message no capture shows.
type NotCaptured struct {
	Key string `json:"key"`
	// Usages is how many current usages the message has; File and Line
	// are the first, Routes every route they name.
	Usages int      `json:"usages"`
	File   string   `json:"file"`
	Line   int      `json:"line"`
	Routes []string `json:"routes,omitempty"`
}

// Cover reports which used messages doc shows. usages are in the order
// the API returned them (the first one per key is the one reported).
func Cover(usages []Usage, doc Document) Coverage {
	visible := doc.VisibleKeys()
	byKey := map[string]*NotCaptured{}
	routes := map[string]map[string]bool{}
	var keys []string
	for _, u := range usages {
		n := byKey[u.Key]
		if n == nil {
			n = &NotCaptured{Key: u.Key, File: u.File, Line: u.Line}
			byKey[u.Key] = n
			routes[u.Key] = map[string]bool{}
			keys = append(keys, u.Key)
		}
		n.Usages++
		if u.Route != "" && !routes[u.Key][u.Route] {
			routes[u.Key][u.Route] = true
			n.Routes = append(n.Routes, u.Route)
		}
	}
	sort.Strings(keys)
	c := Coverage{Messages: len(keys), NotCaptured: []NotCaptured{}}
	for _, k := range keys {
		if visible[k] {
			c.Captured++
			continue
		}
		n := byKey[k]
		sort.Strings(n.Routes)
		c.NotCaptured = append(c.NotCaptured, *n)
	}
	return c
}
