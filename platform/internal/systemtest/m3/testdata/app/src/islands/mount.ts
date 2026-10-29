// Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file.
// Mounts the one React island of the checkout page into an element of
// the Vue tree, sharing the page's runtime.
import { createElement } from "react";
import { createRoot } from "react-dom/client";
import { GlossaProvider, createGlossa } from "@klarlabs-studio/glossa/react";

import { runtime } from "../runtime";
import { PayButton } from "./PayButton";

export function mountPayButton(el: HTMLElement): () => void {
  const glossa = createGlossa({ runtime });
  const root = createRoot(el);
  root.render(createElement(GlossaProvider, { glossa }, createElement(PayButton)));
  return () => root.unmount();
}
