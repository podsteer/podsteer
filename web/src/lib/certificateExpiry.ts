/**
 * Turns a certificate's expiresInSeconds into the sentence an operator reads
 * — "expires in N days" ahead of it, "expired N days ago" behind it.
 *
 * The BACKEND already did the subtraction (domain.Certificate.ExpiresIn,
 * carried across the bridge as expiresInSeconds): this is presentation of an
 * already-computed value, the same QUOTATION formatAge performs on an age in
 * seconds, not a second implementation of the judgement itself.
 */
export function certificateExpiryLabel(expiresInSeconds: number): string {
  const days = Math.floor(Math.abs(expiresInSeconds) / 86400)
  const unit = days === 1 ? 'day' : 'days'

  if (expiresInSeconds < 0) {
    return days === 0 ? 'expired today' : `expired ${days} ${unit} ago`
  }
  return days === 0 ? 'expires today' : `expires in ${days} ${unit}`
}
