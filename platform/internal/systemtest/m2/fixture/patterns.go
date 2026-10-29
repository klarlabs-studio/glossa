package fixture

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// draft is a message rendered in every locale, before parsing.
type draft struct {
	key, ns, pattern, description string
	maxLength                     int
	syntax                        string            // mf1 or mf2, for every locale
	text                          map[string]string // de, en, es, fr, ja
	// informal renders the formal targets with informal address (the
	// formality slip); noMany renders es/fr plurals without `many`.
	informal map[string]string
	noMany   map[string]string
	noun     *noun
	// verb is the action of the "action" pattern.
	verb string
	// hasName: the message has a {name} placeholder to drop.
	hasName bool
	// sensitive: legal text, never sent to a provider.
	sensitive bool
}

// Max lengths of UI elements (characters of visible text).
const (
	maxButton = 40
	maxTitle  = 32
	maxAction = 24
)

func ucfirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// German articles.
func deDefNom(g gender) string { return map[gender]string{masc: "der", fem: "die", neut: "das"}[g] }
func deDefAcc(g gender) string { return map[gender]string{masc: "den", fem: "die", neut: "das"}[g] }
func deDemAcc(g gender) string {
	return map[gender]string{masc: "diesen", fem: "diese", neut: "dieses"}[g]
}
func deNew(g gender) string { return map[gender]string{masc: "Neuer", fem: "Neue", neut: "Neues"}[g] }

// Spanish.
func esDef(g gender) string   { return map[gender]string{masc: "el", fem: "la"}[g] }
func esDefPl(g gender) string { return map[gender]string{masc: "los", fem: "las"}[g] }
func esDem(g gender) string   { return map[gender]string{masc: "este", fem: "esta"}[g] }
func esAll(g gender) string   { return map[gender]string{masc: "Todos los", fem: "Todas las"}[g] }
func esNew(g gender) string   { return map[gender]string{masc: "Nuevo", fem: "Nueva"}[g] }
func esSelected(g gender, plural bool) string {
	s := map[gender]string{masc: "seleccionado", fem: "seleccionada"}[g]
	if plural {
		s += "s"
	}
	return s
}

// French.
func frDef(n frNoun) string {
	switch {
	case n.vowel:
		return "l’"
	case n.g == fem:
		return "la "
	}
	return "le "
}

func frDem(n frNoun) string {
	switch {
	case n.g == fem:
		return "cette "
	case n.vowel:
		return "cet "
	}
	return "ce "
}

func frNone(n frNoun) string {
	if n.g == fem {
		return "aucune"
	}
	return "aucun"
}

func frAll(n frNoun) string {
	if n.g == fem {
		return "Toutes les"
	}
	return "Tous les"
}

func frNew(n frNoun) string {
	switch {
	case n.g == fem:
		return "Nouvelle"
	case n.vowel:
		return "Nouvel"
	}
	return "Nouveau"
}

// frOf is "de " or, before a vowel, "d’".
func frOf(vowel bool) string {
	if vowel {
		return "d’"
	}
	return "de "
}

func frVowelVerb(inf string) bool {
	r, _ := utf8.DecodeRuneInString(inf)
	return strings.ContainsRune("aeiouéèê", r)
}

func frSelected(g gender, plural bool) string {
	s := "sélectionné"
	if g == fem {
		s += "e"
	}
	if plural {
		s += "s"
	}
	return s
}

func frPP(v verb, g gender) string {
	if g == fem {
		return v.frPPf
	}
	return v.frPPm
}

// confirmVerb is the destructive verb a confirmation dialog asks about.
func confirmVerb(n noun) string {
	for _, v := range []string{"delete", "remove", "archive", "revoke", "end"} {
		for _, have := range n.verbs {
			if have == v {
				return v
			}
		}
	}
	return n.verbs[len(n.verbs)-1]
}

// nounDrafts renders every message about one noun.
func nounDrafts(n *noun) []draft {
	var out []draft
	de, en, es, fr, ja := n.de, n.en, n.es, n.fr, n.ja
	key := func(ns, suffix string) string { return ns + "." + n.id + "." + suffix }
	add := func(d draft) {
		d.noun = n
		if d.ns == "" {
			d.ns = n.ns
		}
		if d.syntax == "" {
			d.syntax = "mf1"
		}
		out = append(out, d)
	}
	for _, id := range n.verbs {
		v := verbs[id]
		add(draft{key: key(n.ns, id), pattern: "action", verb: id, maxLength: maxButton,
			description: fmt.Sprintf("Button that %s the %s.", v.enDescribeVerbs, en.sg),
			text: map[string]string{
				"de": de.sg + " " + v.deInf,
				"en": ucfirst(v.enBase) + " " + en.sg,
				"es": ucfirst(v.esInf) + " " + es.sg,
				"fr": ucfirst(v.frInf) + " " + frDef(fr) + fr.sg,
				"ja": ja.w + "を" + v.ja,
			}})
		add(draft{key: key(n.ns, id+"_success"), pattern: "success",
			description: fmt.Sprintf("Toast shown after the %s was %s.", en.sg, v.enPP),
			text: map[string]string{
				"de": ucfirst(deDefNom(de.g)) + " " + de.sg + " wurde " + v.dePP + ".",
				"en": "The " + en.sg + " was " + v.enPP + ".",
				"es": ucfirst(esDef(es.g)) + " " + es.sg + " se ha " + v.esPP + ".",
				"fr": ucfirst(frDef(fr)) + fr.sg + " a été " + frPP(v, fr.g) + ".",
				"ja": ja.w + "を" + v.ja + "しました。",
			}})
	}
	cv := verbs[confirmVerb(*n)]
	add(draft{key: key(n.ns, confirmVerb(*n)+"_confirm"), pattern: "confirm",
		description: fmt.Sprintf("Confirmation dialog before a %s is %s.", en.sg, cv.enPP),
		text: map[string]string{
			"de": "Möchten Sie " + deDemAcc(de.g) + " " + de.sg + " wirklich " + cv.deInf + "?",
			"en": "Do you really want to " + cv.enBase + " this " + en.sg + "?",
			"es": "¿Seguro que desea " + cv.esInf + " " + esDem(es.g) + " " + es.sg + "?",
			"fr": "Voulez-vous vraiment " + cv.frInf + " " + frDem(fr) + fr.sg + " ?",
			"ja": "この" + ja.w + "を" + cv.ja + "してもよろしいですか？",
		},
		informal: map[string]string{
			"es": "¿Seguro que quieres " + cv.esInf + " " + esDem(es.g) + " " + es.sg + "?",
			"fr": "Veux-tu vraiment " + cv.frInf + " " + frDem(fr) + fr.sg + " ?",
			"ja": "この" + ja.w + "を" + cv.ja + "していい？",
		}})
	esMany := "{count, plural, one {# " + es.sg + "} many {# de " + es.pl + "} other {# " + es.pl + "}}"
	frMany := "{count, plural, one {# " + fr.sg + "} many {# " + frOf(fr.vowel) + fr.pl + "} other {# " + fr.pl + "}}"
	add(draft{key: key(n.ns, "count"), pattern: "count",
		description: fmt.Sprintf("Number of %s in the list header. $count is the number.", en.pl),
		text: map[string]string{
			"de": "{count, plural, one {# " + de.sg + "} other {# " + de.pl + "}}",
			"en": "{count, plural, one {# " + en.sg + "} other {# " + en.pl + "}}",
			"es": esMany,
			"fr": frMany,
			"ja": "{count, plural, other {#" + ja.counter + "の" + ja.w + "}}",
		},
		noMany: map[string]string{
			"es": "{count, plural, one {# " + es.sg + "} other {# " + es.pl + "}}",
			"fr": "{count, plural, one {# " + fr.sg + "} other {# " + fr.pl + "}}",
		}})
	add(draft{key: key(n.ns, "empty"), pattern: "empty",
		description: fmt.Sprintf("Empty state of the %s list.", en.pl),
		text: map[string]string{
			"de": "Sie haben noch keine " + de.pl + ".",
			"en": "You have no " + en.pl + " yet.",
			"es": "Todavía no tiene " + es.pl + ".",
			"fr": "Vous n’avez encore " + frNone(fr) + " " + fr.sg + ".",
			"ja": ja.w + "はまだありません。",
		},
		informal: map[string]string{
			"es": "Todavía no tienes " + es.pl + ".",
			"fr": "Tu n’as encore " + frNone(fr) + " " + fr.sg + ".",
			"ja": ja.w + "はまだないよ。",
		}})
	for _, id := range n.verbs[:2] {
		v := verbs[id]
		add(draft{key: key(n.ns, id+"_activity"), pattern: "activity", hasName: true,
			description: fmt.Sprintf("Activity feed entry: a member %s a %s. $name is their display name.", v.enPP, en.sg),
			text: map[string]string{
				"de": "{name} hat " + deDefAcc(de.g) + " " + de.sg + " " + v.dePP + ".",
				"en": "{name} " + v.enPP + " the " + en.sg + ".",
				"es": "{name} ha " + v.esPP + " " + esDef(es.g) + " " + es.sg + ".",
				"fr": "{name} a " + v.frPPm + " " + frDef(fr) + fr.sg + ".",
				"ja": "{name}さんが" + ja.w + "を" + v.ja + "しました。",
			}})
	}
	pv := verbs[n.verbs[0]]
	add(draft{key: key(n.ns, n.verbs[0]+"_permission"), pattern: "permission",
		description: fmt.Sprintf("Permission hint on the %s page. $role is the viewer's role.", en.pl),
		text: map[string]string{
			"de": "{role, select, admin {Als Administrator können Sie " + de.pl + " " + pv.deInf + ".} other {Nur Administratoren können " + de.pl + " " + pv.deInf + ".}}",
			"en": "{role, select, admin {As an administrator, you can " + pv.enBase + " " + en.pl + ".} other {Only administrators can " + pv.enBase + " " + en.pl + ".}}",
			"es": "{role, select, admin {Como administrador, puede " + pv.esInf + " " + esDefPl(es.g) + " " + es.pl + ".} other {Solo los administradores pueden " + pv.esInf + " " + esDefPl(es.g) + " " + es.pl + ".}}",
			"fr": "{role, select, admin {En tant qu’administrateur, vous pouvez " + pv.frInf + " les " + fr.pl + ".} other {Seuls les administrateurs peuvent " + pv.frInf + " les " + fr.pl + ".}}",
			"ja": "{role, select, admin {管理者は" + ja.w + "を" + pv.ja + "できます。} other {" + ja.w + "を" + pv.ja + "できるのは管理者のみです。}}",
		}})
	add(draft{key: key(n.ns, "help"), pattern: "help", syntax: "mf2",
		description: fmt.Sprintf("Help link below the %s list.", en.pl),
		text: map[string]string{
			"de": "Mehr über " + de.pl + " erfahren Sie in der {#link}Hilfe{/link}.",
			"en": "Learn more about " + en.pl + " in the {#link}help center{/link}.",
			"es": "Más información sobre " + esDefPl(es.g) + " " + es.pl + " en el {#link}centro de ayuda{/link}.",
			"fr": "En savoir plus sur les " + fr.pl + " dans le {#link}centre d’aide{/link}.",
			"ja": ja.w + "について詳しくは{#link}ヘルプセンター{/link}をご覧ください。",
		}})
	add(draft{key: key(n.ns, "total"), pattern: "total",
		description: fmt.Sprintf("Total number of %s in the summary panel. $total is the number.", en.pl),
		text: map[string]string{
			"de": "Anzahl der " + de.pl + ": {total, number}",
			"en": "Number of " + en.pl + ": {total, number}",
			"es": "Número de " + es.pl + ": {total, number}",
			"fr": "Nombre " + frOf(fr.vowel) + fr.pl + " : {total, number}",
			"ja": ja.w + "の数: {total, number}",
		}})
	add(draft{key: key(n.ns, "new"), pattern: "new", maxLength: maxTitle,
		description: fmt.Sprintf("Title of the dialog that creates a %s.", en.sg),
		text: map[string]string{
			"de": deNew(de.g) + " " + de.sg,
			"en": "New " + en.sg,
			"es": esNew(es.g) + " " + es.sg,
			"fr": frNew(fr) + " " + fr.sg,
			"ja": "新しい" + ja.w,
		}})
	add(draft{key: key(n.ns, "search"), pattern: "search", maxLength: maxButton,
		description: fmt.Sprintf("Placeholder of the %s search field.", en.pl),
		text: map[string]string{
			"de": de.pl + " durchsuchen",
			"en": "Search " + en.pl,
			"es": "Buscar " + es.pl,
			"fr": "Rechercher des " + fr.pl,
			"ja": ja.w + "を名前で検索",
		}})
	add(draft{key: key(n.ns, "all"), pattern: "list", maxLength: maxTitle,
		description: fmt.Sprintf("Tab that lists all %s.", en.pl),
		text: map[string]string{
			"de": "Alle " + de.pl,
			"en": "All " + en.pl,
			"es": esAll(es.g) + " " + es.pl,
			"fr": frAll(fr) + " " + fr.pl,
			"ja": ja.w + "一覧",
		}})
	add(draft{key: key(n.ns, "selected"), pattern: "selected",
		description: fmt.Sprintf("Toolbar label with the number of selected %s. $count is the number.", en.pl),
		text: map[string]string{
			"de": "{count, plural, one {# " + de.sg + " ausgewählt} other {# " + de.pl + " ausgewählt}}",
			"en": "{count, plural, one {# " + en.sg + " selected} other {# " + en.pl + " selected}}",
			"es": "{count, plural, one {# " + es.sg + " " + esSelected(es.g, false) + "} many {# de " + es.pl + " " + esSelected(es.g, true) + "} other {# " + es.pl + " " + esSelected(es.g, true) + "}}",
			"fr": "{count, plural, one {# " + fr.sg + " " + frSelected(fr.g, false) + "} many {# " + frOf(fr.vowel) + fr.pl + " " + frSelected(fr.g, true) + "} other {# " + fr.pl + " " + frSelected(fr.g, true) + "}}",
			"ja": "{count, plural, other {#" + ja.counter + "の" + ja.w + "を選択中}}",
		},
		noMany: map[string]string{
			"es": "{count, plural, one {# " + es.sg + " " + esSelected(es.g, false) + "} other {# " + es.pl + " " + esSelected(es.g, true) + "}}",
			"fr": "{count, plural, one {# " + fr.sg + " " + frSelected(fr.g, false) + "} other {# " + fr.pl + " " + frSelected(fr.g, true) + "}}",
		}})
	// errors namespace
	add(draft{key: key("errors", "not_found"), ns: "errors", pattern: "not_found",
		description: fmt.Sprintf("Error when a %s does not exist (any more).", en.sg),
		text: map[string]string{
			"de": ucfirst(deDefNom(de.g)) + " " + de.sg + " wurde nicht gefunden.",
			"en": "The " + en.sg + " was not found.",
			"es": "No se encontró " + esDef(es.g) + " " + es.sg + ".",
			"fr": ucfirst(frDef(fr)) + fr.sg + " est introuvable.",
			"ja": ja.w + "が見つかりません。",
		}})
	frImpossible := "Impossible de "
	if frVowelVerb(pv.frInf) {
		frImpossible = "Impossible d’"
	}
	add(draft{key: key("errors", n.verbs[0]+"_failed"), ns: "errors", pattern: "failed",
		description: fmt.Sprintf("Error when a %s could not be %s.", en.sg, pv.enPP),
		text: map[string]string{
			"de": ucfirst(deDefNom(de.g)) + " " + de.sg + " konnte nicht " + pv.dePP + " werden.",
			"en": "The " + en.sg + " could not be " + pv.enPP + ".",
			"es": "No se pudo " + pv.esInf + " " + esDef(es.g) + " " + es.sg + ".",
			"fr": frImpossible + pv.frInf + " " + frDef(fr) + fr.sg + ".",
			"ja": ja.w + "を" + pv.ja + "できませんでした。",
		}})
	return out
}

func actionDrafts() []draft {
	var out []draft
	for _, ns := range actionNamespaces {
		for _, a := range actions {
			out = append(out, draft{key: ns + ".actions." + a.id, ns: ns, pattern: "common_action", syntax: "mf1", maxLength: maxAction,
				description: fmt.Sprintf("Generic %s button in the %s area.", a.id, ns),
				text:        map[string]string{"de": a.de, "en": a.en, "es": a.es, "fr": a.fr, "ja": a.ja}})
		}
	}
	return out
}

func legalDrafts() []draft {
	var out []draft
	for _, c := range legalClauses {
		for _, d := range legalData {
			out = append(out, draft{key: "legal.privacy." + c.id + "_" + d.id, ns: "legal", pattern: "legal_clause", syntax: "mf1", sensitive: true,
				description: fmt.Sprintf("Privacy policy clause (%s) about %s. Legal text: translated by counsel only.", c.id, d.en),
				text: map[string]string{
					"de": fmt.Sprintf(c.de, d.de), "en": fmt.Sprintf(c.en, d.en),
					"es": fmt.Sprintf(c.es, d.es), "fr": fmt.Sprintf(c.fr, d.fr),
				}})
		}
	}
	return out
}
