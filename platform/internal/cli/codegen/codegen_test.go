package codegen_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/cli/codegen"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// catalog is the source the golden files are generated from.
var catalog = map[string]string{
	"cart.checkout":           "Zur Kasse",
	"cart.items":              "{count, plural, one {# item} other {# items}}",
	"checkout.pay":            "Pay {amount, number, ::currency/EUR}",
	"athlete.greeting":        "{gender, select, female {Hi {name}} other {Hello {name}}}",
	"order.shipped":           "Shipped on {date, date, short} at {time, time, short}",
	"checkout.payment-failed": "Payment failed: {reason}",
	"checkout.payment_failed": "Collides with payment-failed",
	"legal.type":              "{type} */ with a comment breaker",
	"nav.home":                "Home",
	"nav.home.title":          "Home title",
}

func entries(t *testing.T) []codegen.Entry {
	t.Helper()
	var out []codegen.Entry
	for k, text := range catalog {
		m, err := mf.ParseMF1(text, "en")
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		out = append(out, codegen.Entry{Key: k, Text: text, Arguments: mf.Arguments(m)})
	}
	return out
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/cli/codegen -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file (go test ./internal/cli/codegen -update):\n%s", name, got)
	}
}

func TestTypeScriptGolden(t *testing.T) {
	ts, warnings := codegen.TypeScript(entries(t), codegen.TSOptions{Source: "locales/en.json"})
	golden(t, "messages.ts", ts)
	golden(t, "glossa-vue.ts", codegen.Vue("messages.ts"))
	golden(t, "glossa-react.ts", codegen.React("messages.ts"))
	var keys []string
	for _, w := range warnings {
		keys = append(keys, w.Key)
	}
	if strings.Join(keys, ",") != "checkout.payment_failed,nav.home" {
		t.Errorf("warnings = %+v", warnings)
	}
}

func TestGoGolden(t *testing.T) {
	src, warnings, err := codegen.Go(entries(t), codegen.GoOptions{Package: "msg", Runtime: "go.klarlabs.de/glossa/runtimes/go", Source: "locales/en.json"})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "messages.go.txt", src)
	if len(warnings) != 1 || warnings[0].Key != "checkout.payment_failed" {
		t.Errorf("warnings = %+v", warnings)
	}
}

func TestDartGolden(t *testing.T) {
	src, warnings := codegen.Dart(entries(t), codegen.DartOptions{Runtime: "package:glossa/glossa.dart", Source: "locales/en.json"})
	golden(t, "messages.dart.txt", src)
	var keys []string
	for _, w := range warnings {
		keys = append(keys, w.Key)
	}
	// The accessor paths are TypeScript's, so the warnings are too.
	if strings.Join(keys, ",") != "checkout.payment_failed,nav.home" {
		t.Errorf("warnings = %+v", warnings)
	}
}

func TestNamesMapAccessorsBackToKeys(t *testing.T) {
	keys := []string{"checkout.pay", "checkout.payment_failed", "cart.items"}
	ts := codegen.TSNames(keys)
	if ts["checkout.paymentFailed"] != "checkout.payment_failed" || ts["cart.items"] != "cart.items" {
		t.Errorf("TSNames = %v", ts)
	}
	gn := codegen.GoNames(keys)
	if gn["CheckoutPaymentFailed"] != "checkout.payment_failed" || gn["CartItems"] != "cart.items" {
		t.Errorf("GoNames = %v", gn)
	}
}

// repoRoot is the monorepo root (it holds pnpm-workspace.yaml).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("not inside the glossa repository")
		}
		dir = parent
	}
}

// TestGeneratedTypeScriptTypeChecks compiles the generated module and
// each framework registration against the real @klarlabs-studio/glossa/vue and
// @klarlabs-studio/glossa/react sources with tsc, plus a consumer whose @ts-expect-error
// lines prove missing and mistyped arguments fail at compile time.
func TestGeneratedTypeScriptTypeChecks(t *testing.T) {
	root := repoRoot(t)
	js := filepath.Join(root, "runtimes", "js")
	ts, _ := codegen.TypeScript(entries(t), codegen.TSOptions{Source: "locales/en.json"})
	for _, fw := range []struct {
		name     string
		render   func(modulePath string) []byte
		consumer string
		// paths maps bare imports of the generated file itself.
		paths map[string]string
	}{
		{name: "vue", render: codegen.Vue, consumer: vueConsumerTS},
		{name: "react", render: codegen.React, consumer: reactConsumerTS,
			paths: map[string]string{"react": filepath.Join(js, "react", "node_modules", "@types", "react")}},
	} {
		t.Run(fw.name, func(t *testing.T) {
			tsc := filepath.Join(js, fw.name, "node_modules", ".bin", "tsc")
			if _, err := os.Stat(tsc); err != nil {
				t.Skip("tsc not installed: run `pnpm install` at the repository root to type-check generated TypeScript")
			}
			dir := t.TempDir()
			write := func(name, body string) {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			registration := "glossa-" + fw.name + ".ts"
			write("messages.ts", string(ts))
			write(registration, string(fw.render("messages.ts")))
			write("consumer.ts", consumerTS+fw.consumer)
			// The registration names the published package (@klarlabs-studio/glossa/vue); the
			// sources it is built from name each other by their workspace names. Both resolve here.
			runtimeSrc := filepath.Join(js, "runtime", "src", "index.ts")
			partsSrc := filepath.Join(js, "elements", "src", "parts.ts")
			frameworkSrc := filepath.Join(js, fw.name, "src", "index.ts")
			paths := map[string]string{
				"@klarlabs-studio/glossa/" + fw.name:     frameworkSrc,
				"@klarlabs-studio/glossa-" + fw.name:     frameworkSrc,
				"@klarlabs-studio/glossa":                runtimeSrc,
				"@klarlabs-studio/glossa-runtime":        runtimeSrc,
				"@klarlabs-studio/glossa/elements/parts": partsSrc,
				"@klarlabs-studio/glossa-elements/parts": partsSrc,
			}
			for k, v := range fw.paths {
				paths[k] = v
			}
			var mapped []string
			for k, v := range paths {
				mapped = append(mapped, fmt.Sprintf("%q: [%q]", k, filepath.ToSlash(v)))
			}
			sort.Strings(mapped)
			write("tsconfig.json", `{
  "compilerOptions": {
    "target": "ES2022", "lib": ["ES2022", "DOM"], "module": "ESNext", "moduleResolution": "Bundler",
    "strict": true, "noUncheckedIndexedAccess": true, "noEmit": true, "skipLibCheck": true, "types": [],
    "isolatedModules": true, "esModuleInterop": true,
    "paths": {`+strings.Join(mapped, ", ")+`}
  },
  "files": ["messages.ts", "`+registration+`", "consumer.ts"]
}`)
			cmd := exec.Command(tsc, "-p", filepath.Join(dir, "tsconfig.json"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tsc: %v\n%s", err, out)
			}
		})
	}
}

const consumerTS = `import { createMessages, type Messages } from "./messages.js";

const t =(id: string, values: Record<string, unknown>): string => id + JSON.stringify(values);
const messages = createMessages(t);

export const ok: string[] = [
  messages.checkout.pay({ amount: 12.5 }),
  messages.cart.items({ count: 3 }),
  messages.cart.checkout(),
  messages.athlete.greeting({ gender: "female", name: "Lina" }),
  messages.athlete.greeting({ gender: "nonbinary", name: "Kim" }),
  messages.order.shipped({ date: new Date(), time: Date.now() }),
  messages.checkout.paymentFailed({ reason: "declined" }),
  messages.legal.type({ type: "x" }),
];

// @ts-expect-error amount is required
messages.checkout.pay({});
// @ts-expect-error amount is a number
messages.checkout.pay({ amount: "12" });
// @ts-expect-error no such message
messages.checkout.refund();
// @ts-expect-error cart.checkout takes no values
messages.cart.checkout({ extra: 1 });
`

const vueConsumerTS = `
import { useTypedMessages } from "./glossa-vue.js";
import { useGlossa } from "@klarlabs-studio/glossa/vue";

// The registration types @klarlabs-studio/glossa/vue's own t().
export function inSetup(): string {
  const m = useTypedMessages();
  const { t: typed } = useGlossa();
  // @ts-expect-error unknown ID once GlossaRegister is augmented
  typed("checkout.refund");
  const key: keyof Messages = "cart.items";
  return m.cart.items({ count: 1 }) + typed(key, { count: 2 });
}
`

const reactConsumerTS = `
import { createElement } from "react";
import { useTypedMessages } from "./glossa-react.js";
import { T, useGlossa } from "@klarlabs-studio/glossa/react";

// The registration types @klarlabs-studio/glossa/react's own t() and <T>.
export function Component(): string {
  const m = useTypedMessages();
  const { t: typed } = useGlossa();
  // @ts-expect-error unknown ID once GlossaRegister is augmented
  typed("checkout.refund");
  // @ts-expect-error <T id> is checked too
  createElement(T, { id: "checkout.refund" });
  // @ts-expect-error and so are its values
  createElement(T, { id: "cart.items", values: { count: "2" } });
  createElement(T, { id: "cart.items", values: { count: 2 } }, "2 items");
  const key: keyof Messages = "cart.items";
  return m.cart.items({ count: 1 }) + typed(key, { count: 2 });
}
`

// TestGeneratedGoCompiles builds the generated file against the real Go
// runtime in a scratch module.
func TestGeneratedGoCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a scratch module")
	}
	root := repoRoot(t)
	src, _, err := codegen.Go(entries(t), codegen.GoOptions{Package: "msg", Runtime: "go.klarlabs.de/glossa/runtimes/go", Source: "locales/en.json"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runtimeDir := filepath.Join(root, "runtimes", "go")
	gomod, err := os.ReadFile(filepath.Join(runtimeDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	gosum, _ := os.ReadFile(filepath.Join(runtimeDir, "go.sum"))
	// The runtime's own requirements, plus replaces to the repo checkout.
	mod := strings.Replace(string(gomod), "module go.klarlabs.de/glossa/runtimes/go", "module scratch", 1)
	mod = strings.ReplaceAll(mod, "=> ../../messageformat", "=> "+filepath.Join(root, "messageformat"))
	mod += "\nrequire go.klarlabs.de/glossa/runtimes/go v0.0.0\n" +
		"replace go.klarlabs.de/glossa/runtimes/go => " + runtimeDir + "\n"
	write("go.mod", mod)
	write("go.sum", string(gosum))
	write("msg/messages.go", string(src))
	write("main.go", `package main

import (
	"context"
	"fmt"
	"time"

	glossa "go.klarlabs.de/glossa/runtimes/go"

	"scratch/msg"
)

func main() {
	client, err := glossa.New(glossa.Config{EdgeURL: "http://127.0.0.1:1", DeliveryKey: "pk", Environment: "production"})
	if err != nil {
		panic(err)
	}
	defer client.Close()
	m := msg.For(client.For("en"))
	fmt.Println(m.CheckoutPay(12.5), m.CartItems(3), m.CartCheckout(), m.AthleteGreeting("female", "Lina"),
		m.OrderShipped(time.Now(), time.Now()), msg.IDCheckoutPay)
	fmt.Println(msg.FromContext(context.Background(), client).CheckoutPaymentFailed("declined"))
}
`)
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated Go doesn't compile: %v\n%s\n%s", err, out, src)
	}
}

// TestGeneratedDartAnalyzes analyses the generated module and a consumer
// of it against the real Dart runtime in a scratch package, and checks
// that `dart format` leaves the generated file alone — it carries
// `// dart format off`, because this generator lays it out itself and a
// reformat would make `glossa generate --check` fail for ever after.
func TestGeneratedDartAnalyzes(t *testing.T) {
	if testing.Short() {
		t.Skip("resolves a scratch package")
	}
	dart, err := exec.LookPath("dart")
	if err != nil {
		// A silent skip is how "the generated Dart compiles" stayed
		// unverified: the Go jobs have no Dart SDK, so this test never
		// ran anywhere. One job installs one and asks for it by name —
		// the same rule `capturetest.StartChrome` applies to Chrome.
		//
		// The flag is GLOSSA_REQUIRE_DART and not CI, because every job
		// sets CI: keying on it made this fail in the platform job,
		// which has no SDK and is not the job that owns this check. One
		// job demands the SDK; everywhere else says plainly why it
		// skipped.
		if os.Getenv("GLOSSA_REQUIRE_DART") == "1" {
			t.Fatalf("GLOSSA_REQUIRE_DART=1 and no Dart SDK on PATH: %v", err)
		}
		t.Skip("dart not installed: install the Dart SDK to analyse generated Dart")
	}
	root := repoRoot(t)
	src, _ := codegen.Dart(entries(t), codegen.DartOptions{Runtime: "package:glossa/glossa.dart", Source: "locales/en.json"})
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pubspec.yaml", "name: scratch\nenvironment:\n  sdk: ^3.13.0\ndependencies:\n  glossa:\n    path: "+
		filepath.ToSlash(filepath.Join(root, "runtimes", "dart"))+"\n")
	write("lib/messages.dart", string(src))
	write("lib/consumer.dart", dartConsumer)

	for _, step := range [][]string{
		{"pub", "get"},
		{"analyze", "--fatal-infos"},
		{"format", "--output=none", "--set-exit-if-changed", "lib/messages.dart"},
	} {
		cmd := exec.Command(dart, step...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dart %s: %v\n%s\n%s", strings.Join(step, " "), err, out, src)
		}
	}
}

const dartConsumer = `import 'package:glossa/glossa.dart';

import 'messages.dart';

/// Every accessor, with the argument types the generator chose.
String demo(GlossaClient client) {
  final messages = Messages.of(client);
  return messages.checkout.pay(amount: 12.5) +
      messages.cart.items(count: 3) +
      messages.cart.checkout() +
      messages.athlete.greeting(gender: 'female', name: 'Lina') +
      messages.order.shipped(date: DateTime.now(), time: DateTime.now()) +
      messages.checkout.paymentFailed(reason: 'declined') +
      messages.nav.home.title(defaultText: 'Home title') +
      messages.legal.type(type: 'x') +
      MessageIds.checkoutPay;
}

/// The accessors also bind to a bare translate function, which is how a
/// Flutter app reaches them (GlossaScope.of(context).t).
String withLocalizer(Localizer localizer) =>
    Messages(localizer.t).cart.checkout();
`
