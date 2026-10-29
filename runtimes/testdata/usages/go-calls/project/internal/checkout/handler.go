// Package checkout renders the checkout pages.
package checkout

import (
	"context"
	"net/http"
	"strings"

	glossa "go.klarlabs.de/glossa/runtimes/go"

	"example.com/shop/internal/msg"
)

// Package-level initializers belong to no function: no component.
var fallbackTitle = glossa.Default("Checkout")

var titles = map[string]func(l *glossa.Localizer) string{
	"cart": func(l *glossa.Localizer) string { return l.T("cart.title", nil) },
}

// Handler serves the payment page.
type Handler struct {
	client *glossa.Client
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	title := h.client.T(r.Context(), "checkout.title", nil, fallbackTitle)
	l := h.client.Localizer(r.Context())
	pay := l.T(
		"checkout.pay",
		glossa.Args{"amount": 12.5},
	)
	m := msg.FromContext(r.Context(), h.client)
	_, _ = w.Write([]byte(title + pay + m.CartItems(3)))
}

// Service renders labels for one locale.
type Service struct {
	client *glossa.Client
}

func (s Service) Label(locale string) string {
	return s.client.For(locale).T("cart.checkout", nil)
}

func (s Service) Items(ctx context.Context) []string {
	render := func(n int) string {
		return msg.For(s.client.Localizer(ctx)).CheckoutPay(float64(n))
	}
	return []string{render(1), s.client.For("de").T(`checkout.raw-string`, nil)}
}

// Summary shows how accessor names are matched: m.Title() is the `title`
// message, strings.Title is a package function.
func Summary(l *glossa.Localizer, name string) string {
	m := msg.For(l)
	return "Größe 🥨" + m.Title() + strings.Title(name) + l.T("cart.summary", nil)
}
