import { describe, expect, it } from "vitest";
import { capture, region, usage } from "../test/fake-context";
import {
  aspectRatio,
  boxStyle,
  captureFor,
  cropFor,
  CROP_PADDING,
  groupUsages,
  imageStyle,
  localesOf,
  MIN_CROP,
  pageSize,
  repositoryLink,
  screensOf,
  usageLocation,
  visibleRegions,
} from "./context";

describe("groupUsages", () => {
  it("groups application → route → component, keeping the API's order", () => {
    const groups = groupUsages(
      [
        usage({ key: "a", route: "/checkout", component: "PaymentFooter", file: "src/PaymentFooter.vue", line: 42 }),
        usage({ key: "a", route: "/checkout", component: "PaymentFooter", file: "src/PaymentFooter.vue", line: 44 }),
        usage({ key: "a", route: "/checkout", component: "Summary", file: "src/Summary.vue", line: 7 }),
        usage({ key: "a", route: "/cart", component: "Cart", file: "src/Cart.vue", line: 3 }),
        usage({ key: "a", application_id: "a2", file: "mail/welcome.go", line: 9 }),
      ],
      (id) => (id === "a1" ? "Web" : "Mailer"),
    );
    expect(groups.map((g) => g.application)).toEqual(["Web", "Mailer"]);
    expect(groups[0]!.routes.map((r) => r.route)).toEqual(["/checkout", "/cart"]);
    expect(groups[0]!.routes[0]!.components.map((c) => c.component)).toEqual(["PaymentFooter", "Summary"]);
    expect(groups[0]!.routes[0]!.components[0]!.usages.map(usageLocation)).toEqual(["src/PaymentFooter.vue:42", "src/PaymentFooter.vue:44"]);
    // Nothing known about the route or the component: one group each, named by the pane.
    expect(groups[1]!.routes[0]!.route).toBeUndefined();
    expect(groups[1]!.routes[0]!.components[0]!.component).toBeUndefined();
  });

  it("names an application by its ID while the names aren't loaded", () => {
    expect(groupUsages([usage({ key: "a" })])[0]!.application).toBe("a1");
  });
});

describe("repositoryLink", () => {
  const u = usage({ key: "a", file: "src/checkout/Pay.vue", line: 42, commit: "9f2c1e7" });

  it("is nothing while no Git connection exists", () => {
    expect(repositoryLink(u)).toBeUndefined();
    expect(repositoryLink(u, { webUrl: "" })).toBeUndefined();
  });

  it("points at blob/<commit>/<file>#L<line> on the connected repository", () => {
    expect(repositoryLink(u, { webUrl: "https://github.com/acme/shop/" })).toBe("https://github.com/acme/shop/blob/9f2c1e7/src/checkout/Pay.vue#L42");
  });
});

describe("screens", () => {
  const de = capture("c1", ["pay"], { locale: "de" }).capture;
  const en = capture("c2", ["pay"], { locale: "en" }).capture;
  const phone = capture("c3", ["pay"], { locale: "de", viewport: { width: 390, height: 844 } }).capture;
  const other = capture("c4", ["pay"], { locale: "de", route: "/cart" }).capture;

  it("groups captures by application, route and viewport", () => {
    const screens = screensOf([de, en, phone, other]);
    expect(screens.map((s) => s.id)).toEqual(["a1|/checkout|1280x800", "a1|/checkout|390x844", "a1|/cart|1280x800"]);
    expect([...screens[0]!.byLocale.keys()]).toEqual(["de", "en"]);
    expect(localesOf([de, en, phone, other])).toEqual(["de", "en"]);
  });

  it("shows the wanted locale, else the source's, else whatever it has", () => {
    const screen = screensOf([de, en])[0]!;
    expect(captureFor(screen, "en", "de")).toBe(en);
    expect(captureFor(screen, "fr", "de")).toBe(de);
    expect(captureFor(screensOf([en])[0]!, "fr", "de")).toBe(en);
  });
});

describe("crop", () => {
  // A 1280×800 viewport photographed at 2560×4800: a 2× device scale factor, a 2400 CSS-pixel page.
  const shot = capture("c1", ["pay"], {
    image: { digest: "d", width: 2560, height: 4800, url: "/v1/img" },
    regions: [region({ box: { x: 600, y: 1000, width: 100, height: 20 } })],
  }).capture;

  it("measures the page in CSS pixels, not image pixels", () => {
    expect(pageSize(shot)).toEqual({ x: 0, y: 0, width: 1280, height: 2400 });
  });

  it("pads the region, keeps a minimum and stays inside the page", () => {
    const crop = cropFor(shot)!;
    expect(crop.width).toBe(Math.max(MIN_CROP.width, 100 + 2 * CROP_PADDING.x));
    expect(crop.height).toBe(MIN_CROP.height);
    // Centred on the region.
    expect(crop.x + crop.width / 2).toBe(650);
    expect(crop.y + crop.height / 2).toBe(1010);
    const top = cropFor({ ...shot, regions: [region({ box: { x: 0, y: 0, width: 10, height: 10 } })] })!;
    expect(top.x).toBe(0);
    expect(top.y).toBe(0);
  });

  it("has nothing to crop around when no region is visible", () => {
    expect(cropFor({ ...shot, regions: [region({ visible: false })] })).toBeUndefined();
    expect(cropFor({ ...shot, regions: [region({ box: { x: 0, y: 0, width: 0, height: 0 } })] })).toBeUndefined();
    expect(visibleRegions([region(), region({ visible: false })])).toHaveLength(1);
  });

  it("places the image and the boxes in percentages of the crop", () => {
    const crop = { x: 100, y: 200, width: 400, height: 200 };
    expect(imageStyle(crop, shot)).toEqual({ width: "320.0000%", left: "-25.0000%", top: "-100.0000%" });
    expect(boxStyle({ x: 100, y: 200, width: 200, height: 100 }, crop)).toEqual({ left: "0.0000%", top: "0.0000%", width: "50.0000%", height: "50.0000%" });
    expect(aspectRatio(crop)).toBe("400 / 200");
  });
});
