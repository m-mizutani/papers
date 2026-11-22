// TypeScript port of pkg/domain/model/order.go (fractional index keys).
const DIGITS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz'

function midpoint(a: string, b: string): string {
  if (b !== '') {
    let n = 0
    while (n < b.length && (a[n] ?? DIGITS[0]) === b[n]) n++
    if (n > 0) return b.slice(0, n) + midpoint(a.slice(n), b.slice(n))
  }
  const da = a === '' ? 0 : DIGITS.indexOf(a[0] as string)
  const db = b === '' ? DIGITS.length : DIGITS.indexOf(b[0] as string)
  if (db - da > 1) return DIGITS[Math.floor((da + db + 1) / 2)] as string
  if (b !== '' && b.length > 1) return b.slice(0, 1)
  return (DIGITS[da] as string) + midpoint(a.slice(1), '')
}

export function keyBetween(a: string, b: string): string {
  if (a !== '' && b !== '' && a >= b) throw new Error('lower key must be less than upper key')
  return midpoint(a, b)
}
