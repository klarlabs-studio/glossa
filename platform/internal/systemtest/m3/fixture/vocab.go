package fixture

// The vocabulary of Brotwerk's shop: the things on its screens, in the
// source locale (de) and the four targets (en, es, fr, ja). Every
// message of the fixture is composed from these tables by the patterns
// in patterns.go, so the five locales agree by construction and the
// fixture can predict what the platform will hold.

// words is one concept in the five locales, in the order
// de, en, es, fr, ja.
type words struct{ de, en, es, fr, ja string }

// at returns the word in a locale.
func (w words) at(locale string) string {
	switch locale {
	case "de":
		return w.de
	case "en":
		return w.en
	case "es":
		return w.es
	case "fr":
		return w.fr
	case "ja":
		return w.ja
	}
	return w.de
}

// Locales are the fixture's locales, source first.
var Locales = []string{"de", "en", "es", "fr", "ja"}

// SourceLocale is the locale the shop is written in.
const SourceLocale = "de"

// TargetLocales are the locales translated from the source.
var TargetLocales = []string{"en", "es", "fr", "ja"}

// nouns name what the shop sells and shows. Every one carries its
// plural in the locales that need one for a plural message.
var nouns = []words{
	{"Brot", "bread", "pan", "pain", "パン"},
	{"Brötchen", "roll", "panecillo", "petit pain", "ロールパン"},
	{"Kuchen", "cake", "pastel", "gâteau", "ケーキ"},
	{"Torte", "tart", "tarta", "tarte", "タルト"},
	{"Keks", "biscuit", "galleta", "biscuit", "ビスケット"},
	{"Croissant", "croissant", "cruasán", "croissant", "クロワッサン"},
	{"Baguette", "baguette", "baguette", "baguette", "バゲット"},
	{"Zopf", "plait", "trenza", "tresse", "編みパン"},
	{"Stollen", "stollen", "stollen", "stollen", "シュトレン"},
	{"Bagel", "bagel", "bagel", "bagel", "ベーグル"},
	{"Muffin", "muffin", "magdalena", "muffin", "マフィン"},
	{"Waffel", "waffle", "gofre", "gaufre", "ワッフル"},
	{"Semmel", "bun", "bollo", "boule", "バンズ"},
	{"Laib", "loaf", "hogaza", "miche", "ひと塊"},
	{"Kruste", "crust", "corteza", "croûte", "クラスト"},
	{"Teig", "dough", "masa", "pâte", "生地"},
	{"Sauerteig", "sourdough", "masa madre", "levain", "サワードウ"},
	{"Mehl", "flour", "harina", "farine", "小麦粉"},
	{"Hefe", "yeast", "levadura", "levure", "酵母"},
	{"Salz", "salt", "sal", "sel", "塩"},
	{"Korn", "grain", "grano", "grain", "穀物"},
	{"Ofen", "oven", "horno", "four", "オーブン"},
	{"Blech", "tray", "bandeja", "plaque", "天板"},
	{"Filiale", "branch", "sucursal", "boutique", "店舗"},
	{"Lieferung", "delivery", "entrega", "livraison", "配達"},
	{"Abholung", "pickup", "recogida", "retrait", "受け取り"},
	{"Bestellung", "order", "pedido", "commande", "注文"},
	{"Warenkorb", "basket", "cesta", "panier", "カート"},
	{"Rechnung", "invoice", "factura", "facture", "請求書"},
	{"Zahlung", "payment", "pago", "paiement", "支払い"},
	{"Gutschein", "voucher", "vale", "bon", "クーポン"},
	{"Konto", "account", "cuenta", "compte", "アカウント"},
	{"Adresse", "address", "dirección", "adresse", "住所"},
	{"Versand", "shipping", "envío", "expédition", "配送"},
	{"Abo", "subscription", "suscripción", "abonnement", "定期購入"},
	{"Hinweis", "note", "aviso", "note", "お知らせ"},
	{"Zutat", "ingredient", "ingrediente", "ingrédient", "材料"},
	{"Allergen", "allergen", "alérgeno", "allergène", "アレルゲン"},
	{"Bewertung", "review", "reseña", "avis", "レビュー"},
	{"Frage", "question", "pregunta", "question", "質問"},
}

// nounPlurals are the plurals of nouns, by the same index. Japanese
// has none; the plural patterns use a counter instead.
var nounPlurals = []words{
	{"Brote", "breads", "panes", "pains", ""},
	{"Brötchen", "rolls", "panecillos", "petits pains", ""},
	{"Kuchen", "cakes", "pasteles", "gâteaux", ""},
	{"Torten", "tarts", "tartas", "tartes", ""},
	{"Kekse", "biscuits", "galletas", "biscuits", ""},
	{"Croissants", "croissants", "cruasanes", "croissants", ""},
	{"Baguettes", "baguettes", "baguettes", "baguettes", ""},
	{"Zöpfe", "plaits", "trenzas", "tresses", ""},
	{"Stollen", "stollen", "stollen", "stollen", ""},
	{"Bagels", "bagels", "bagels", "bagels", ""},
	{"Muffins", "muffins", "magdalenas", "muffins", ""},
	{"Waffeln", "waffles", "gofres", "gaufres", ""},
	{"Semmeln", "buns", "bollos", "boules", ""},
	{"Laibe", "loaves", "hogazas", "miches", ""},
	{"Krusten", "crusts", "cortezas", "croûtes", ""},
	{"Teige", "doughs", "masas", "pâtes", ""},
	{"Sauerteige", "sourdoughs", "masas madre", "levains", ""},
	{"Mehle", "flours", "harinas", "farines", ""},
	{"Hefen", "yeasts", "levaduras", "levures", ""},
	{"Salze", "salts", "sales", "sels", ""},
	{"Körner", "grains", "granos", "grains", ""},
	{"Öfen", "ovens", "hornos", "fours", ""},
	{"Bleche", "trays", "bandejas", "plaques", ""},
	{"Filialen", "branches", "sucursales", "boutiques", ""},
	{"Lieferungen", "deliveries", "entregas", "livraisons", ""},
	{"Abholungen", "pickups", "recogidas", "retraits", ""},
	{"Bestellungen", "orders", "pedidos", "commandes", ""},
	{"Warenkörbe", "baskets", "cestas", "paniers", ""},
	{"Rechnungen", "invoices", "facturas", "factures", ""},
	{"Zahlungen", "payments", "pagos", "paiements", ""},
	{"Gutscheine", "vouchers", "vales", "bons", ""},
	{"Konten", "accounts", "cuentas", "comptes", ""},
	{"Adressen", "addresses", "direcciones", "adresses", ""},
	{"Versandarten", "shipping options", "envíos", "expéditions", ""},
	{"Abos", "subscriptions", "suscripciones", "abonnements", ""},
	{"Hinweise", "notes", "avisos", "notes", ""},
	{"Zutaten", "ingredients", "ingredientes", "ingrédients", ""},
	{"Allergene", "allergens", "alérgenos", "allergènes", ""},
	{"Bewertungen", "reviews", "reseñas", "avis", ""},
	{"Fragen", "questions", "preguntas", "questions", ""},
}

// counters are the Japanese counter words the plural patterns use.
var counters = []string{"個", "件", "点"}

// verbs are what people do on the screens.
var verbs = []words{
	{"bestellen", "order", "pedir", "commander", "注文"},
	{"bezahlen", "pay", "pagar", "payer", "支払い"},
	{"abholen", "collect", "recoger", "retirer", "受け取り"},
	{"speichern", "save", "guardar", "enregistrer", "保存"},
	{"entfernen", "remove", "quitar", "retirer", "削除"},
	{"hinzufügen", "add", "añadir", "ajouter", "追加"},
	{"ändern", "change", "cambiar", "modifier", "変更"},
	{"prüfen", "check", "comprobar", "vérifier", "確認"},
	{"teilen", "share", "compartir", "partager", "共有"},
	{"wiederholen", "retry", "reintentar", "réessayer", "再試行"},
}

// participles are the German past participles of verbs, by infinitive.
var participles = map[string]string{
	"bestellen":   "bestellt",
	"bezahlen":    "bezahlt",
	"abholen":     "abgeholt",
	"speichern":   "gespeichert",
	"entfernen":   "entfernt",
	"hinzufügen":  "hinzugefügt",
	"ändern":      "geändert",
	"prüfen":      "geprüft",
	"teilen":      "geteilt",
	"wiederholen": "wiederholt",
}

// adjectives colour the headings and the blurbs.
var adjectives = []words{
	{"frisch", "fresh", "fresco", "frais", "新鮮な"},
	{"warm", "warm", "caliente", "chaud", "温かい"},
	{"knusprig", "crisp", "crujiente", "croustillant", "サクサクの"},
	{"herzhaft", "hearty", "sabroso", "savoureux", "香ばしい"},
	{"süß", "sweet", "dulce", "sucré", "甘い"},
	{"täglich", "daily", "diario", "quotidien", "毎日の"},
	{"regional", "regional", "regional", "régional", "地域の"},
	{"handgemacht", "handmade", "artesanal", "artisanal", "手作りの"},
}
