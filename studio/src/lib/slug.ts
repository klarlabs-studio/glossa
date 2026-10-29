/** A URL slug from a name: "Checkout App!" → "checkout-app". Matches the contract's slug pattern. */
export function slugify(name: string): string {
  return name
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63)
    .replace(/-+$/, "");
}

export const SLUG_PATTERN = "[a-z0-9]([a-z0-9\\-]{0,61}[a-z0-9])?";
