/** Emailed links carry their token in the fragment (`/auth/sign-in#token=…`), so it never reaches a server log. */
export function tokenFromHash(hash: string): string | undefined {
  const params = new URLSearchParams(hash.replace(/^#/, ""));
  const token = params.get("token");
  return token && token.length <= 256 ? token : undefined;
}

/** Only same-origin paths are followed after sign-in — never `//evil.example` or `https://…`. */
export function safeNext(next: unknown): string | undefined {
  if (typeof next !== "string" || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return undefined;
  return next;
}

/** Drop the token from the address bar and history as soon as it's read. */
export function forgetHash(): void {
  history.replaceState(history.state, "", location.pathname + location.search);
}
