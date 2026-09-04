/**
 * RFC 4180 CSV rendering, shared by every table page's Export CSV control.
 *
 * Each page builds its own header and rows — the columns it currently shows,
 * the rows currently matching the search, in the current sort order — and
 * hands them here rather than reimplementing quoting seven times. Keeping
 * this the only place that emits a comma is what makes a label that happens
 * to contain one a display detail instead of a corrupted file.
 */

/** A field must be quoted when it contains the comma, the quote itself, or
    either line-ending character — the four characters RFC 4180 calls out. */
function needsQuoting(field: string): boolean {
  return /["\r\n,]/.test(field)
}

/** Quotes a field when it needs it, doubling any quote inside. */
function quoteField(field: string): string {
  if (!needsQuoting(field)) return field
  return `"${field.replace(/"/g, '""')}"`
}

/**
 * A field beginning with `=`, `+`, `-`, `@`, a tab or a carriage return opens
 * a FORMULA to whatever spreadsheet opens this file — Excel, Sheets and
 * Numbers all launch one from any of these. Every field PodSteer puts in an
 * export comes from the cluster — a label value, an event message, a node's
 * own annotation — so it is UNTRUSTED INPUT to that spreadsheet even though
 * it is not untrusted to PodSteer, which never evaluates it. A leading
 * apostrophe is the spreadsheet convention for "read this as text": it is
 * invisible once opened, and it is the whole guard.
 */
const FORMULA_PREFIXES = ['=', '+', '-', '@', '\t', '\r']

function neutraliseFormula(field: string): string {
  return FORMULA_PREFIXES.some((prefix) => field.startsWith(prefix)) ? `'${field}` : field
}

/** A row shorter than the header, padded with empty fields rather than
    rejected — the export should never be pickier than the table it came
    from. A row longer than the header is left alone: it is not a shape this
    codebase produces, and truncating it would silently drop a value nobody
    asked to lose. */
function padRow(row: string[], length: number): string[] {
  if (row.length >= length) return row
  return row.concat(Array<string>(length - row.length).fill(''))
}

/**
 * Renders `columns` as the header row and `rows` beneath it: RFC 4180
 * quoting, CRLF line endings (including after the final row), UTF-8 with no
 * byte-order mark — a BOM is an Excel-on-Windows convenience this project has
 * no reason to assume, and prepending one would corrupt the file for
 * anything that takes UTF-8 literally.
 *
 * The CSV-injection guard runs on every field, header included: a generic
 * table's columns are the API server's own printed names, which for a CRD
 * come from whatever its author chose — cluster-controlled, the same as a
 * cell.
 */
export function toCSV(columns: string[], rows: string[][]): string {
  const lines = [columns, ...rows.map((row) => padRow(row, columns.length))]
  return (
    lines
      .map((line) => line.map((field) => quoteField(neutraliseFormula(field))).join(','))
      .join('\r\n') + '\r\n'
  )
}
