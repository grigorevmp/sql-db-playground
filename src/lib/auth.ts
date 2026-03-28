export const hashPassword = async (value: string) => {
  const encoder = new TextEncoder()
  const bytes = encoder.encode(value)
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}
