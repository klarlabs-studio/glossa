package fixture

// The vocabulary of a small, realistic SaaS product: its entities
// (nouns), what users do with them (verbs), and the termbase concepts
// among them. Every message of the fixture is rendered from these tables
// by the patterns in patterns.go, in the source locale (de) and the four
// target locales (en, es, fr, ja), so the reference translations are
// consistent by construction and can be checked mechanically.

// gender is a grammatical gender: m, f or n (German only).
type gender string

const (
	masc gender = "m"
	fem  gender = "f"
	neut gender = "n"
)

type deNoun struct {
	sg, pl string
	g      gender
}

type enNoun struct{ sg, pl string }

type esNoun struct {
	sg, pl string
	g      gender
}

type frNoun struct {
	sg, pl string
	g      gender
	// vowel: the noun starts with a vowel sound (l’, cet, d’, Nouvel).
	vowel bool
}

type jaNoun struct {
	w string
	// counter is the counter word of plural counts: 件, 人, 個.
	counter string
}

type noun struct {
	id string
	ns string
	de deNoun
	en enNoun
	es esNoun
	fr frNoun
	ja jaNoun
	// verbs apply to the noun, the most typical first.
	verbs []string
	// concept is the termbase concept the noun is ("" when none).
	concept string
}

type verb struct {
	deInf, dePP     string
	enBase, enPP    string
	esInf, esPP     string
	frInf           string
	frPPm, frPPf    string
	ja              string // a する-verb stem: ダウンロード, 削除
	enDescribeVerbs string // "downloads", for descriptions
}

var verbs = map[string]verb{
	"download": {"herunterladen", "heruntergeladen", "download", "downloaded", "descargar", "descargado", "télécharger", "téléchargé", "téléchargée", "ダウンロード", "downloads"},
	"delete":   {"löschen", "gelöscht", "delete", "deleted", "eliminar", "eliminado", "supprimer", "supprimé", "supprimée", "削除", "deletes"},
	"edit":     {"bearbeiten", "bearbeitet", "edit", "edited", "editar", "editado", "modifier", "modifié", "modifiée", "編集", "edits"},
	"share":    {"teilen", "geteilt", "share", "shared", "compartir", "compartido", "partager", "partagé", "partagée", "共有", "shares"},
	"create":   {"erstellen", "erstellt", "create", "created", "crear", "creado", "créer", "créé", "créée", "作成", "creates"},
	"export":   {"exportieren", "exportiert", "export", "exported", "exportar", "exportado", "exporter", "exporté", "exportée", "エクスポート", "exports"},
	"archive":  {"archivieren", "archiviert", "archive", "archived", "archivar", "archivado", "archiver", "archivé", "archivée", "アーカイブ", "archives"},
	"update":   {"aktualisieren", "aktualisiert", "update", "updated", "actualizar", "actualizado", "mettre à jour", "mis à jour", "mise à jour", "更新", "updates"},
	"renew":    {"verlängern", "verlängert", "renew", "renewed", "renovar", "renovado", "renouveler", "renouvelé", "renouvelée", "更新", "renews"},
	"redeem":   {"einlösen", "eingelöst", "redeem", "redeemed", "canjear", "canjeado", "utiliser", "utilisé", "utilisée", "適用", "redeems"},
	"reset":    {"zurücksetzen", "zurückgesetzt", "reset", "reset", "restablecer", "restablecido", "réinitialiser", "réinitialisé", "réinitialisée", "リセット", "resets"},
	"end":      {"beenden", "beendet", "end", "ended", "cerrar", "cerrado", "fermer", "fermé", "fermée", "終了", "ends"},
	"revoke":   {"widerrufen", "widerrufen", "revoke", "revoked", "revocar", "revocado", "révoquer", "révoqué", "révoquée", "無効化", "revokes"},
	"resend":   {"erneut senden", "erneut gesendet", "resend", "resent", "reenviar", "reenviado", "renvoyer", "renvoyé", "renvoyée", "再送信", "resends"},
	"invite":   {"einladen", "eingeladen", "invite", "invited", "invitar", "invitado", "inviter", "invité", "invitée", "招待", "invites"},
	"remove":   {"entfernen", "entfernt", "remove", "removed", "quitar", "quitado", "retirer", "retiré", "retirée", "削除", "removes"},
	"mute":     {"stummschalten", "stummgeschaltet", "mute", "muted", "silenciar", "silenciado", "mettre en sourdine", "mis en sourdine", "mise en sourdine", "ミュート", "mutes"},
}

var nouns = []noun{
	// billing
	{id: "invoice", ns: "billing", concept: "invoice", verbs: []string{"download", "delete", "share", "export"},
		de: deNoun{"Rechnung", "Rechnungen", fem}, en: enNoun{"invoice", "invoices"}, es: esNoun{"factura", "facturas", fem},
		fr: frNoun{"facture", "factures", fem, false}, ja: jaNoun{"請求書", "件"}},
	{id: "subscription", ns: "billing", concept: "subscription", verbs: []string{"renew", "edit", "archive", "delete"},
		de: deNoun{"Abonnement", "Abonnements", neut}, en: enNoun{"subscription", "subscriptions"}, es: esNoun{"suscripción", "suscripciones", fem},
		fr: frNoun{"abonnement", "abonnements", masc, true}, ja: jaNoun{"サブスクリプション", "件"}},
	{id: "payment_method", ns: "billing", concept: "payment_method", verbs: []string{"create", "edit", "update", "delete"},
		de: deNoun{"Zahlungsmethode", "Zahlungsmethoden", fem}, en: enNoun{"payment method", "payment methods"}, es: esNoun{"método de pago", "métodos de pago", masc},
		fr: frNoun{"moyen de paiement", "moyens de paiement", masc, false}, ja: jaNoun{"支払い方法", "件"}},
	{id: "discount_code", ns: "billing", concept: "discount_code", verbs: []string{"redeem", "create", "share", "delete"},
		de: deNoun{"Rabattcode", "Rabattcodes", masc}, en: enNoun{"discount code", "discount codes"}, es: esNoun{"código de descuento", "códigos de descuento", masc},
		fr: frNoun{"code promo", "codes promo", masc, false}, ja: jaNoun{"割引コード", "件"}},
	{id: "plan", ns: "billing", concept: "plan", verbs: []string{"edit", "share", "export", "archive"},
		de: deNoun{"Tarif", "Tarife", masc}, en: enNoun{"plan", "plans"}, es: esNoun{"plan", "planes", masc},
		fr: frNoun{"forfait", "forfaits", masc, false}, ja: jaNoun{"プラン", "件"}},
	// auth
	{id: "password", ns: "auth", concept: "password", verbs: []string{"reset", "update", "create", "delete"},
		de: deNoun{"Passwort", "Passwörter", neut}, en: enNoun{"password", "passwords"}, es: esNoun{"contraseña", "contraseñas", fem},
		fr: frNoun{"mot de passe", "mots de passe", masc, false}, ja: jaNoun{"パスワード", "件"}},
	{id: "session", ns: "auth", concept: "session", verbs: []string{"end", "revoke", "export", "delete"},
		de: deNoun{"Sitzung", "Sitzungen", fem}, en: enNoun{"session", "sessions"}, es: esNoun{"sesión", "sesiones", fem},
		fr: frNoun{"session", "sessions", fem, false}, ja: jaNoun{"セッション", "件"}},
	{id: "api_key", ns: "auth", concept: "api_key", verbs: []string{"create", "revoke", "update", "delete"},
		de: deNoun{"API-Schlüssel", "API-Schlüssel", masc}, en: enNoun{"API key", "API keys"}, es: esNoun{"clave de API", "claves de API", fem},
		fr: frNoun{"clé API", "clés API", fem, false}, ja: jaNoun{"APIキー", "件"}},
	{id: "passkey", ns: "auth", verbs: []string{"create", "revoke", "delete"},
		de: deNoun{"Passkey", "Passkeys", masc}, en: enNoun{"passkey", "passkeys"}, es: esNoun{"llave de acceso", "llaves de acceso", fem},
		fr: frNoun{"clé d’accès", "clés d’accès", fem, false}, ja: jaNoun{"パスキー", "件"}},
	{id: "invitation", ns: "auth", concept: "invitation", verbs: []string{"resend", "revoke", "create", "delete"},
		de: deNoun{"Einladung", "Einladungen", fem}, en: enNoun{"invitation", "invitations"}, es: esNoun{"invitación", "invitaciones", fem},
		fr: frNoun{"invitation", "invitations", fem, true}, ja: jaNoun{"招待", "件"}},
	// dashboard
	{id: "report", ns: "dashboard", concept: "report", verbs: []string{"download", "export", "share", "create"},
		de: deNoun{"Bericht", "Berichte", masc}, en: enNoun{"report", "reports"}, es: esNoun{"informe", "informes", masc},
		fr: frNoun{"rapport", "rapports", masc, false}, ja: jaNoun{"レポート", "件"}},
	{id: "widget", ns: "dashboard", verbs: []string{"create", "edit", "update", "delete"},
		de: deNoun{"Widget", "Widgets", neut}, en: enNoun{"widget", "widgets"}, es: esNoun{"widget", "widgets", masc},
		fr: frNoun{"widget", "widgets", masc, false}, ja: jaNoun{"ウィジェット", "個"}},
	{id: "project", ns: "dashboard", concept: "project", verbs: []string{"create", "archive", "share", "delete"},
		de: deNoun{"Projekt", "Projekte", neut}, en: enNoun{"project", "projects"}, es: esNoun{"proyecto", "proyectos", masc},
		fr: frNoun{"projet", "projets", masc, false}, ja: jaNoun{"プロジェクト", "件"}},
	{id: "chart", ns: "dashboard", verbs: []string{"export", "download", "edit", "share"},
		de: deNoun{"Diagramm", "Diagramme", neut}, en: enNoun{"chart", "charts"}, es: esNoun{"gráfico", "gráficos", masc},
		fr: frNoun{"graphique", "graphiques", masc, false}, ja: jaNoun{"グラフ", "個"}},
	{id: "filter", ns: "dashboard", verbs: []string{"create", "edit", "share", "delete"},
		de: deNoun{"Filter", "Filter", masc}, en: enNoun{"filter", "filters"}, es: esNoun{"filtro", "filtros", masc},
		fr: frNoun{"filtre", "filtres", masc, false}, ja: jaNoun{"フィルター", "個"}},
	// settings
	{id: "workspace", ns: "settings", concept: "workspace", verbs: []string{"create", "archive", "edit", "delete"},
		de: deNoun{"Arbeitsbereich", "Arbeitsbereiche", masc}, en: enNoun{"workspace", "workspaces"}, es: esNoun{"espacio de trabajo", "espacios de trabajo", masc},
		fr: frNoun{"espace de travail", "espaces de travail", masc, true}, ja: jaNoun{"ワークスペース", "件"}},
	{id: "member", ns: "settings", concept: "member", verbs: []string{"invite", "remove", "edit"},
		de: deNoun{"Mitglied", "Mitglieder", neut}, en: enNoun{"member", "members"}, es: esNoun{"miembro", "miembros", masc},
		fr: frNoun{"membre", "membres", masc, false}, ja: jaNoun{"メンバー", "人"}},
	{id: "role", ns: "settings", concept: "role", verbs: []string{"create", "edit", "delete"},
		de: deNoun{"Rolle", "Rollen", fem}, en: enNoun{"role", "roles"}, es: esNoun{"rol", "roles", masc},
		fr: frNoun{"rôle", "rôles", masc, false}, ja: jaNoun{"ロール", "件"}},
	{id: "integration", ns: "settings", concept: "integration", verbs: []string{"create", "update", "edit", "delete"},
		de: deNoun{"Integration", "Integrationen", fem}, en: enNoun{"integration", "integrations"}, es: esNoun{"integración", "integraciones", fem},
		fr: frNoun{"intégration", "intégrations", fem, true}, ja: jaNoun{"連携", "件"}},
	{id: "webhook", ns: "settings", concept: "webhook", verbs: []string{"create", "edit", "update", "delete"},
		de: deNoun{"Webhook", "Webhooks", masc}, en: enNoun{"webhook", "webhooks"}, es: esNoun{"webhook", "webhooks", masc},
		fr: frNoun{"webhook", "webhooks", masc, false}, ja: jaNoun{"Webhook", "件"}},
	// notifications
	{id: "notification", ns: "notifications", concept: "notification", verbs: []string{"mute", "archive", "delete"},
		de: deNoun{"Benachrichtigung", "Benachrichtigungen", fem}, en: enNoun{"notification", "notifications"}, es: esNoun{"notificación", "notificaciones", fem},
		fr: frNoun{"notification", "notifications", fem, false}, ja: jaNoun{"通知", "件"}},
	{id: "comment", ns: "notifications", verbs: []string{"create", "edit", "delete"},
		de: deNoun{"Kommentar", "Kommentare", masc}, en: enNoun{"comment", "comments"}, es: esNoun{"comentario", "comentarios", masc},
		fr: frNoun{"commentaire", "commentaires", masc, false}, ja: jaNoun{"コメント", "件"}},
	{id: "file", ns: "notifications", concept: "file", verbs: []string{"download", "share", "archive", "delete"},
		de: deNoun{"Datei", "Dateien", fem}, en: enNoun{"file", "files"}, es: esNoun{"archivo", "archivos", masc},
		fr: frNoun{"fichier", "fichiers", masc, false}, ja: jaNoun{"ファイル", "件"}},
	{id: "task", ns: "notifications", verbs: []string{"create", "edit", "archive", "delete"},
		de: deNoun{"Aufgabe", "Aufgaben", fem}, en: enNoun{"task", "tasks"}, es: esNoun{"tarea", "tareas", fem},
		fr: frNoun{"tâche", "tâches", fem, false}, ja: jaNoun{"タスク", "件"}},
	{id: "reminder", ns: "notifications", verbs: []string{"create", "edit", "mute", "delete"},
		de: deNoun{"Erinnerung", "Erinnerungen", fem}, en: enNoun{"reminder", "reminders"}, es: esNoun{"recordatorio", "recordatorios", masc},
		fr: frNoun{"rappel", "rappels", masc, false}, ja: jaNoun{"リマインダー", "件"}},
}

// termForms are a concept's terms in one locale: the preferred term and
// its plural (an admitted term when it differs), and the forbidden term
// and its plural.
type termForms struct {
	pref, prefPl string
	forb, forbPl string
}

type concept struct {
	id, domain, definition string
	terms                  map[string]termForms // by locale
}

// concepts is the termbase: 20 concepts, each with the preferred term per
// locale and, for the target locales, a forbidden one — the words a
// careless translation (or an older product's memory) would use.
var concepts = []concept{
	{"invoice", "billing", "A bill for a period of a subscription.", map[string]termForms{
		"de": {"Rechnung", "Rechnungen", "", ""}, "en": {"invoice", "invoices", "bill", "bills"},
		"es": {"factura", "facturas", "recibo", "recibos"}, "fr": {"facture", "factures", "note", "notes"}, "ja": {"請求書", "", "勘定書", ""}}},
	{"subscription", "billing", "A recurring paid plan of a workspace.", map[string]termForms{
		"de": {"Abonnement", "Abonnements", "", ""}, "en": {"subscription", "subscriptions", "membership", "memberships"},
		"es": {"suscripción", "suscripciones", "abono", "abonos"}, "fr": {"abonnement", "abonnements", "souscription", "souscriptions"}, "ja": {"サブスクリプション", "", "定期購読", ""}}},
	{"payment_method", "billing", "A card or account that pays invoices.", map[string]termForms{
		"de": {"Zahlungsmethode", "Zahlungsmethoden", "", ""}, "en": {"payment method", "payment methods", "payment option", "payment options"},
		"es": {"método de pago", "métodos de pago", "forma de pago", "formas de pago"}, "fr": {"moyen de paiement", "moyens de paiement", "mode de règlement", "modes de règlement"}, "ja": {"支払い方法", "", "決済手段", ""}}},
	{"discount_code", "billing", "A code that lowers the price of a plan.", map[string]termForms{
		"de": {"Rabattcode", "Rabattcodes", "", ""}, "en": {"discount code", "discount codes", "coupon", "coupons"},
		"es": {"código de descuento", "códigos de descuento", "cupón", "cupones"}, "fr": {"code promo", "codes promo", "coupon", "coupons"}, "ja": {"割引コード", "", "クーポン", ""}}},
	{"plan", "billing", "A price tier with its limits.", map[string]termForms{
		"de": {"Tarif", "Tarife", "", ""}, "en": {"plan", "plans", "tier", "tiers"},
		"es": {"plan", "planes", "tarifa", "tarifas"}, "fr": {"forfait", "forfaits", "formule", "formules"}, "ja": {"プラン", "", "料金体系", ""}}},
	{"password", "security", "The secret a person signs in with.", map[string]termForms{
		"de": {"Passwort", "Passwörter", "", ""}, "en": {"password", "passwords", "passcode", "passcodes"},
		"es": {"contraseña", "contraseñas", "clave", "claves"}, "fr": {"mot de passe", "mots de passe", "code secret", "codes secrets"}, "ja": {"パスワード", "", "暗証番号", ""}}},
	{"session", "security", "A signed-in browser or device.", map[string]termForms{
		"de": {"Sitzung", "Sitzungen", "", ""}, "en": {"session", "sessions", "login", "logins"},
		"es": {"sesión", "sesiones", "conexión", "conexiones"}, "fr": {"session", "sessions", "connexion", "connexions"}, "ja": {"セッション", "", "接続", ""}}},
	{"api_key", "security", "A secret that authenticates API requests.", map[string]termForms{
		"de": {"API-Schlüssel", "", "", ""}, "en": {"API key", "API keys", "API token", "API tokens"},
		"es": {"clave de API", "claves de API", "llave de API", "llaves de API"}, "fr": {"clé API", "clés API", "jeton API", "jetons API"}, "ja": {"APIキー", "", "APIトークン", ""}}},
	{"invitation", "security", "An offer to join a workspace.", map[string]termForms{
		"de": {"Einladung", "Einladungen", "", ""}, "en": {"invitation", "invitations", "", ""},
		"es": {"invitación", "invitaciones", "convite", "convites"}, "fr": {"invitation", "invitations", "convocation", "convocations"}, "ja": {"招待", "", "招待状", ""}}},
	{"report", "analytics", "A saved view of metrics.", map[string]termForms{
		"de": {"Bericht", "Berichte", "", ""}, "en": {"report", "reports", "", ""},
		"es": {"informe", "informes", "reporte", "reportes"}, "fr": {"rapport", "rapports", "compte rendu", "comptes rendus"}, "ja": {"レポート", "", "報告書", ""}}},
	{"project", "workspace", "A body of work inside a workspace.", map[string]termForms{
		"de": {"Projekt", "Projekte", "", ""}, "en": {"project", "projects", "", ""},
		"es": {"proyecto", "proyectos", "encargo", "encargos"}, "fr": {"projet", "projets", "chantier", "chantiers"}, "ja": {"プロジェクト", "", "案件", ""}}},
	{"workspace", "workspace", "The top-level container of a team.", map[string]termForms{
		"de": {"Arbeitsbereich", "Arbeitsbereiche", "", ""}, "en": {"workspace", "workspaces", "", ""},
		"es": {"espacio de trabajo", "espacios de trabajo", "área de trabajo", "áreas de trabajo"}, "fr": {"espace de travail", "espaces de travail", "zone de travail", "zones de travail"}, "ja": {"ワークスペース", "", "作業スペース", ""}}},
	{"member", "workspace", "A person with access to a workspace.", map[string]termForms{
		"de": {"Mitglied", "Mitglieder", "", ""}, "en": {"member", "members", "", ""},
		"es": {"miembro", "miembros", "socio", "socios"}, "fr": {"membre", "membres", "adhérent", "adhérents"}, "ja": {"メンバー", "", "会員", ""}}},
	{"role", "workspace", "A named set of permissions.", map[string]termForms{
		"de": {"Rolle", "Rollen", "", ""}, "en": {"role", "roles", "", ""},
		"es": {"rol", "roles", "papel", "papeles"}, "fr": {"rôle", "rôles", "fonction", "fonctions"}, "ja": {"ロール", "", "役割", ""}}},
	{"integration", "workspace", "A connection to another product.", map[string]termForms{
		"de": {"Integration", "Integrationen", "", ""}, "en": {"integration", "integrations", "", ""},
		"es": {"integración", "integraciones", "conector", "conectores"}, "fr": {"intégration", "intégrations", "connecteur", "connecteurs"}, "ja": {"連携", "", "統合", ""}}},
	{"webhook", "workspace", "An HTTP callback for events.", map[string]termForms{
		"de": {"Webhook", "Webhooks", "", ""}, "en": {"webhook", "webhooks", "", ""},
		"es": {"webhook", "webhooks", "gancho web", "ganchos web"}, "fr": {"webhook", "webhooks", "crochet web", "crochets web"}, "ja": {"Webhook", "", "ウェブフック", ""}}},
	{"notification", "messaging", "A message about activity.", map[string]termForms{
		"de": {"Benachrichtigung", "Benachrichtigungen", "", ""}, "en": {"notification", "notifications", "", ""},
		"es": {"notificación", "notificaciones", "aviso", "avisos"}, "fr": {"notification", "notifications", "avis", ""}, "ja": {"通知", "", "お知らせ", ""}}},
	{"file", "messaging", "An uploaded document or attachment.", map[string]termForms{
		"de": {"Datei", "Dateien", "", ""}, "en": {"file", "files", "", ""},
		"es": {"archivo", "archivos", "fichero", "ficheros"}, "fr": {"fichier", "fichiers", "document", "documents"}, "ja": {"ファイル", "", "書類", ""}}},
	{"administrator", "workspace", "A member who manages the workspace.", map[string]termForms{
		"de": {"Administrator", "Administratoren", "", ""}, "en": {"administrator", "administrators", "", ""},
		"es": {"administrador", "administradores", "gestor", "gestores"}, "fr": {"administrateur", "administrateurs", "gestionnaire", "gestionnaires"}, "ja": {"管理者", "", "アドミン", ""}}},
	{"help_center", "support", "The product's documentation site.", map[string]termForms{
		"de": {"Hilfe", "", "", ""}, "en": {"help center", "", "", ""},
		"es": {"centro de ayuda", "", "centro de soporte", ""}, "fr": {"centre d’aide", "", "centre de support", ""}, "ja": {"ヘルプセンター", "", "サポートセンター", ""}}},
}

// actions are the common buttons every product namespace repeats: the
// same source in several namespaces is what translation memory reuses
// exactly.
var actions = []struct {
	id                 string
	de, en, es, fr, ja string
}{
	{"save", "Speichern", "Save", "Guardar", "Enregistrer", "保存"},
	{"cancel", "Abbrechen", "Cancel", "Cancelar", "Annuler", "キャンセル"},
	{"back", "Zurück", "Back", "Atrás", "Retour", "戻る"},
	{"continue", "Weiter", "Continue", "Continuar", "Continuer", "次へ"},
	{"close", "Schließen", "Close", "Cerrar", "Fermer", "閉じる"},
	{"retry", "Wiederholen", "Retry", "Reintentar", "Réessayer", "もう一度試す"},
}

var actionNamespaces = []string{"billing", "auth", "dashboard", "settings", "notifications"}

// Legal clauses (the sensitive namespace): 8 clause templates × 5 kinds
// of personal data. Humans translate them; they never reach a provider.
var legalClauses = []struct {
	id             string
	de, en, es, fr string // %s is the data kind
}{
	{"purpose", "Wir verarbeiten Ihre %s ausschließlich zur Vertragserfüllung.", "We process your %s solely to perform the contract.",
		"Tratamos sus %s exclusivamente para la ejecución del contrato.", "Nous traitons vos %s uniquement pour l’exécution du contrat."},
	{"retention", "Ihre %s werden nach Vertragsende gelöscht.", "Your %s are deleted when the contract ends.",
		"Sus %s se eliminan al finalizar el contrato.", "Vos %s sont supprimées à la fin du contrat."},
	{"objection", "Sie können der Verarbeitung Ihrer %s jederzeit widersprechen.", "You may object to the processing of your %s at any time.",
		"Puede oponerse al tratamiento de sus %s en cualquier momento.", "Vous pouvez vous opposer à tout moment au traitement de vos %s."},
	{"third_parties", "Wir geben Ihre %s nicht an Dritte weiter.", "We do not share your %s with third parties.",
		"No cedemos sus %s a terceros.", "Nous ne transmettons pas vos %s à des tiers."},
	{"location", "Ihre %s werden in der Europäischen Union gespeichert.", "Your %s are stored in the European Union.",
		"Sus %s se almacenan en la Unión Europea.", "Vos %s sont stockées dans l’Union européenne."},
	{"access", "Sie haben das Recht auf Auskunft über Ihre %s.", "You have the right to access your %s.",
		"Tiene derecho a acceder a sus %s.", "Vous avez le droit d’accéder à vos %s."},
	{"basis", "Die Verarbeitung Ihrer %s erfolgt auf Grundlage von Art. 6 DSGVO.", "Your %s are processed on the basis of Art. 6 GDPR.",
		"El tratamiento de sus %s se basa en el art. 6 del RGPD.", "Le traitement de vos %s repose sur l’art. 6 du RGPD."},
	{"acceptance", "Mit der Nutzung von {product} akzeptieren Sie die Regeln zu Ihren %s.", "By using {product}, you accept the rules on your %s.",
		"Al usar {product}, acepta las normas sobre sus %s.", "En utilisant {product}, vous acceptez les règles relatives à vos %s."},
}

var legalData = []struct {
	id             string
	de, en, es, fr string
}{
	{"account", "Kontodaten", "account data", "datos de cuenta", "données de compte"},
	{"billing", "Rechnungsdaten", "billing data", "datos de facturación", "données de facturation"},
	{"usage", "Nutzungsdaten", "usage data", "datos de uso", "données d’utilisation"},
	{"payment", "Zahlungsdaten", "payment data", "datos de pago", "données de paiement"},
	{"communication", "Kommunikationsdaten", "communication data", "datos de comunicación", "données de communication"},
}
