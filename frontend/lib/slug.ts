// A workspace's address, as the API checks it (internal/domain/workspace.go).

export const MIN_SLUG = 3;
export const MAX_SLUG = 40;
const PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Why an address is not valid, or null if it is. */
export function slugProblem(slug: string): string | null {
  if (slug.length < MIN_SLUG || slug.length > MAX_SLUG) return `Entre ${MIN_SLUG} y ${MAX_SLUG} caracteres`;
  if (!PATTERN.test(slug)) return "Solo minúsculas sin acentos, números y guiones entre palabras";
  return null;
}

/**
 * An address from a name, like the API makes one: "Diseño de producto" is
 * "diseno-de-producto". The API may still number it if it is taken.
 */
export function slugify(name: string): string {
  const words = name
    .toLowerCase()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
  let slug = "";
  for (const w of words) {
    const next = slug ? `${slug}-${w}` : w;
    if (next.length > MAX_SLUG - 5) {
      slug = slug || w.slice(0, MAX_SLUG - 5);
      break;
    }
    slug = next;
  }
  if (!slug) return "";
  return slug.length < MIN_SLUG ? `ws-${slug}` : slug;
}

/** What a typed address becomes: lowercase, spaces as dashes. */
export function normalizeSlug(text: string) {
  return text.toLowerCase().replace(/\s+/g, "-");
}
