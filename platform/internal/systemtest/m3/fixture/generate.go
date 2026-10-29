package fixture

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// Patterns the generator gives a message. The pattern decides the
// message's MessageFormat shape and how the app renders it, and so which
// kind of usage and which kind of region it produces.
const (
	// PatternPlain is a label: one literal, rendered by t().
	PatternPlain = "plain"
	// PatternValues takes a name, rendered by t() with values.
	PatternValues = "values"
	// PatternPlural selects on a count, rendered by t().
	PatternPlural = "plural"
	// PatternAttribute is a placeholder or a title: an attribute region.
	PatternAttribute = "attribute"
	// PatternComponent is a label rendered by <GlossaText>/<T>: the host
	// element's box is the region.
	PatternComponent = "component"
	// PatternMarkup holds markup and is rendered by <GlossaText>/<T>.
	PatternMarkup = "markup"
)

// patternCycle is the order the generator hands patterns out in. Two
// thirds are t() calls, because that is how most copy is written.
var patternCycle = []string{
	PatternPlain, PatternComponent, PatternPlain, PatternPlural,
	PatternValues, PatternPlain, PatternAttribute, PatternComponent,
	PatternPlain, PatternMarkup, PatternValues, PatternPlain,
}

// area is one file of the app and how many messages it holds.
type area struct {
	namespace string
	file      string
	component string
	route     string
	url       string
	title     string
	count     int
	// page says the file is a routed page (a Vue SFC under src/pages).
	page bool
	// island says the file is the React island.
	island bool
}

// areas is the shape of the app: two shared components, eight routed
// pages and one React island on the checkout route. 150 messages.
var areas = []area{
	{namespace: "layout", file: "src/components/SiteHeader.vue", component: "SiteHeader", count: 8},
	{namespace: "layout", file: "src/components/SiteFooter.vue", component: "SiteFooter", count: 6},
	{namespace: "home", file: "src/pages/HomePage.vue", component: "HomePage", route: "/", url: "/", title: "Brotwerk", count: 18, page: true},
	{namespace: "products", file: "src/pages/ProductsPage.vue", component: "ProductsPage", route: "/produkte", url: "/produkte", title: "Sortiment", count: 20, page: true},
	{namespace: "product", file: "src/pages/ProductPage.vue", component: "ProductPage", route: "/produkte/[id]", url: "/produkte/sauerteig", title: "Produkt", count: 18, page: true},
	{namespace: "cart", file: "src/pages/CartPage.vue", component: "CartPage", route: "/warenkorb", url: "/warenkorb", title: "Warenkorb", count: 18, page: true},
	{namespace: "checkout", file: "src/pages/CheckoutPage.vue", component: "CheckoutPage", route: "/kasse", url: "/kasse", title: "Kasse", count: 14, page: true},
	{namespace: "checkout", file: "src/islands/PayButton.tsx", component: "PayButton", route: "/kasse", count: 6, island: true},
	{namespace: "account", file: "src/pages/AccountPage.vue", component: "AccountPage", route: "/konto", url: "/konto", title: "Konto", count: 16, page: true},
	{namespace: "help", file: "src/pages/HelpPage.vue", component: "HelpPage", route: "/hilfe", url: "/hilfe", title: "Hilfe", count: 14, page: true},
	{namespace: "invoice", file: "src/pages/InvoicePage.vue", component: "InvoicePage", route: "/rechnung", url: "/rechnung", title: "Rechnung", count: 12, page: true},
}

// goReuse says how much of the server side reuses the web copy: the
// receipt renderer calls the first goCalls invoice messages, and its
// template prints the rest of the namespace plus the footer's.
const (
	goCallsFile     = "internal/receipt/receipt.go"
	goCallsFunc     = "receipt.Render"
	goTemplateFile  = "templates/receipt.html.tmpl"
	goCalls         = 7
	goTemplateExtra = 3
)

// newKeyNames are the five keys the pull request's CI push adds.
var newKeyNames = []string{
	"pickup.window", "pickup.slots", "pickup.reminder", "pickup.branch", "pickup.confirm",
}

// invalidIndex is the new key whose first push is refused by the
// structural check: its plural selector has no catch-all variant.
const invalidIndex = 2

// Generate builds the fixture. The output is a pure function of the
// seed; the seed only picks vocabulary.
func Generate(seed int64) (*Fixture, error) {
	rng := rand.New(rand.NewPCG(uint64(seed), 0x6d33))
	f := &Fixture{
		Seed:           seed,
		SourceLocale:   SourceLocale,
		Locales:        append([]string(nil), TargetLocales...),
		Application:    Application,
		Commit:         Commit,
		Branch:         DefaultBranch,
		Viewports:      [][2]int{{1280, 800}, {390, 844}},
		CaptureLocales: []string{"de", "ja"},
	}
	for _, a := range areas {
		if a.page {
			f.Pages = append(f.Pages, Page{Route: a.route, URL: a.url, File: a.file, Component: a.component, Title: a.title})
		}
	}

	used := map[string]bool{}
	n := 0
	for _, a := range areas {
		for i := range a.count {
			pattern := patternCycle[n%len(patternCycle)]
			// The island's copy is short: no markup, no attributes.
			if a.island && (pattern == PatternMarkup || pattern == PatternAttribute) {
				pattern = PatternPlain
			}
			m := compose(rng, a.namespace, pattern, used)
			m.Web = Surface{File: a.file, Component: a.component, Route: a.route, Kind: kindOf(pattern)}
			f.Messages = append(f.Messages, m)
			n++
			_ = i
		}
	}

	// The server side reuses the invoice copy and the footer's.
	invoice, footer := byNamespace(f.Messages, "invoice"), byNamespace(f.Messages, "layout")
	for i, m := range invoice {
		if i < goCalls {
			m.Go = append(m.Go, Surface{File: goCallsFile, Component: goCallsFunc, Kind: KindT})
			continue
		}
		m.Go = append(m.Go, Surface{File: goTemplateFile, Kind: KindTemplate})
	}
	for i, m := range footer {
		if i >= goTemplateExtra {
			break
		}
		m.Go = append(m.Go, Surface{File: goTemplateFile, Kind: KindTemplate})
	}

	// What the pull request adds: five pickup keys on the checkout page.
	for i, key := range newKeyNames {
		pattern := PatternPlain
		if i == invalidIndex {
			pattern = PatternPlural
		}
		m := composeKey(rng, key, pattern, "pickup")
		m.Web = Surface{File: "src/pages/CheckoutPage.vue", Component: "CheckoutPage", Route: "/kasse", Kind: kindOf(pattern)}
		f.NewKeys = append(f.NewKeys, m)
	}
	invalid := &f.NewKeys[invalidIndex]
	f.InvalidKey = invalid.Key
	f.RepairedSource = invalid.Source
	// A plural without a catch-all variant: the structural check refuses
	// it, and the PR check annotates the line it is used on.
	f.InvalidSource = strings.Replace(invalid.Source, "\n* {{", "\nthree {{", 1)
	f.InvalidFile = invalid.Web.File

	if len(f.Messages) != 150 {
		return nil, fmt.Errorf("the fixture has %d messages, the RFC says about 150", len(f.Messages))
	}
	if err := setLines(f); err != nil {
		return nil, err
	}
	return f, nil
}

// byNamespace are the messages of one namespace, in order, as pointers.
func byNamespace(messages []Message, ns string) []*Message {
	var out []*Message
	for i := range messages {
		if messages[i].Namespace == ns {
			out = append(out, &messages[i])
		}
	}
	return out
}

// kindOf is the usage kind a pattern produces on the web side.
func kindOf(pattern string) string {
	switch pattern {
	case PatternComponent, PatternMarkup:
		return KindComponent
	default:
		return KindT
	}
}

// compose builds one message of a namespace with a pattern, picking a
// noun that the namespace hasn't used yet.
func compose(rng *rand.Rand, namespace, pattern string, used map[string]bool) Message {
	for {
		i := rng.IntN(len(nouns))
		key := namespace + "." + slug(nouns[i].en) + "." + suffix(pattern)
		if used[key] {
			continue
		}
		used[key] = true
		return build(key, namespace, pattern, i, rng)
	}
}

// composeKey builds a message with a given key.
func composeKey(rng *rand.Rand, key, pattern, namespace string) Message {
	return build(key, namespace, pattern, rng.IntN(len(nouns)), rng)
}

// suffix names the pattern in a key, the way product copy is organized.
func suffix(pattern string) string {
	switch pattern {
	case PatternPlural:
		return "count"
	case PatternValues:
		return "activity"
	case PatternAttribute:
		return "placeholder"
	case PatternMarkup:
		return "help"
	case PatternComponent:
		return "label"
	default:
		return "title"
	}
}

// slug is an English noun as a key segment.
func slug(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "-")
}

// build renders one message in the five locales.
func build(key, namespace, pattern string, noun int, rng *rand.Rand) Message {
	m := Message{Key: key, Namespace: namespace, Pattern: pattern, Translations: map[string]string{}}
	adj := adjectives[rng.IntN(len(adjectives))]
	verb := verbs[rng.IntN(len(verbs))]
	counter := counters[noun%len(counters)]
	for _, locale := range Locales {
		text := render(pattern, locale, nouns[noun], nounPlurals[noun], adj, verb, counter)
		if locale == SourceLocale {
			m.Source = text
			continue
		}
		m.Translations[locale] = text
	}
	return m
}

// render writes one message in one locale.
func render(pattern, locale string, noun, plural, adj, verb words, counter string) string {
	n, p, a, v := noun.at(locale), plural.at(locale), adj.at(locale), verb.at(locale)
	switch pattern {
	case PatternPlural:
		return pluralPattern(locale, n, p, counter)
	case PatternValues:
		switch locale {
		case "de":
			return "{$name} hat " + n + " " + participles[v] + "."
		case "en":
			return "{$name} " + v + "ed the " + n + "."
		case "es":
			return "{$name} ha " + v + " " + n + "."
		case "fr":
			return "{$name} a " + v + " " + n + "."
		default:
			return "{$name} さんが" + n + "を" + v + "しました。"
		}
	case PatternAttribute:
		switch locale {
		case "de":
			return n + " suchen …"
		case "en":
			return "Search " + p + " …"
		case "es":
			return "Buscar " + p + " …"
		case "fr":
			return "Rechercher des " + p + " …"
		default:
			return n + "を検索…"
		}
	case PatternMarkup:
		switch locale {
		case "de":
			return "Mehr über " + n + " lesen Sie im {#link}Hilfebereich{/link}."
		case "en":
			return "Read more about " + p + " in the {#link}help centre{/link}."
		case "es":
			return "Más sobre " + p + " en el {#link}centro de ayuda{/link}."
		case "fr":
			return "En savoir plus sur les " + p + " dans le {#link}centre d’aide{/link}."
		default:
			return n + "については{#link}ヘルプ{/link}をご覧ください。"
		}
	case PatternComponent:
		// A badge, so no locale needs adjective agreement.
		if locale == "fr" {
			return n + " : " + a
		}
		if locale == "ja" {
			return n + "：" + a
		}
		return n + ": " + a
	default:
		switch locale {
		case "de":
			return n + " " + v
		case "en":
			return v + " " + n
		case "es":
			return v + " " + n
		case "fr":
			return v + " " + n
		default:
			return n + "を" + v
		}
	}
}

// pluralPattern writes a plural message with the locale's CLDR
// categories: de and en have one and other, es and fr add many, ja has
// other only and counts with a counter word.
func pluralPattern(locale, noun, plural, counter string) string {
	var b strings.Builder
	b.WriteString(".input {$count :number}\n.match $count\n")
	switch locale {
	case "de":
		b.WriteString("one {{{$count} " + noun + "}}\n* {{{$count} " + plural + "}}")
	case "en":
		b.WriteString("one {{{$count} " + noun + "}}\n* {{{$count} " + plural + "}}")
	case "es":
		b.WriteString("one {{{$count} " + noun + "}}\nmany {{{$count} de " + plural + "}}\n* {{{$count} " + plural + "}}")
	case "fr":
		b.WriteString("one {{{$count} " + noun + "}}\nmany {{{$count} de " + plural + "}}\n* {{{$count} " + plural + "}}")
	default:
		b.WriteString("* {{{$count}" + counter + "の" + noun + "}}")
	}
	return b.String()
}
