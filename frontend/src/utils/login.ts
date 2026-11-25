/**
 * The sign-in page that comes back to the current page afterwards. The query
 * is kept: the OAuth consent screen (/oauth/authorize) is all query.
 */
export function loginPath(pathname: string, search: string, error?: string): string {
  return `/login?return_to=${encodeURIComponent(pathname + search)}${error ? `&error=${encodeURIComponent(error)}` : ''}`
}
