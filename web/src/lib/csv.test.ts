import { describe, expect, it } from 'vitest'
import { toCSV } from './csv'

describe('toCSV', () => {
  it('renders a header and rows, CRLF-terminated', () => {
    const csv = toCSV(['Name', 'Namespace'], [['web-1', 'default']])

    expect(csv).toBe('Name,Namespace\r\nweb-1,default\r\n')
  })

  it('quotes a field containing a comma', () => {
    const csv = toCSV(['Message'], [['pulling, then starting']])

    expect(csv).toBe('Message\r\n"pulling, then starting"\r\n')
  })

  it('quotes a field containing a double quote, doubling it', () => {
    const csv = toCSV(['Message'], [['said "hello"']])

    expect(csv).toBe('Message\r\n"said ""hello"""\r\n')
  })

  it('quotes a field containing an embedded newline', () => {
    const csv = toCSV(['Message'], [['line one\nline two']])

    expect(csv).toBe('Message\r\n"line one\nline two"\r\n')
  })

  it('quotes a field containing an embedded carriage return', () => {
    const csv = toCSV(['Message'], [['line one\rline two']])

    expect(csv).toBe('Message\r\n"line one\rline two"\r\n')
  })

  it('leaves a plain field unquoted', () => {
    const csv = toCSV(['Name'], [['web-1']])

    expect(csv).toBe('Name\r\nweb-1\r\n')
  })

  it('renders an empty table as just the header', () => {
    const csv = toCSV(['Name', 'Namespace'], [])

    expect(csv).toBe('Name,Namespace\r\n')
  })

  it('pads a row shorter than the header with empty fields', () => {
    // The generic table's rows come from whatever the server printed for
    // that particular object, and nothing here guarantees every row carries
    // a cell for every column the kind's other rows happen to have.
    const csv = toCSV(['Name', 'Status', 'Age'], [['web-1', 'Running']])

    expect(csv).toBe('Name,Status,Age\r\nweb-1,Running,\r\n')
  })

  it('carries no byte-order mark', () => {
    const csv = toCSV(['Name'], [['web-1']])

    expect(csv.charCodeAt(0)).not.toBe(0xfeff)
    expect(csv.startsWith('﻿')).toBe(false)
    expect(csv.startsWith('Name')).toBe(true)
  })

  describe('the CSV-injection guard', () => {
    // A field beginning with any of these opens a formula in Excel, Sheets
    // or Numbers. Every one of them is content PodSteer read off the
    // cluster — a label, an annotation, an event message — so it is
    // untrusted input to whichever spreadsheet opens the export, never
    // executed by PodSteer itself.
    it.each([['='], ['+'], ['-'], ['@'], ['\t']])(
      'prefixes a field starting with %j with an apostrophe',
      (prefix) => {
        const csv = toCSV(['Value'], [[`${prefix}cmd|'/bin/sh'!A1`]])

        expect(csv).toBe(`Value\r\n'${prefix}cmd|'/bin/sh'!A1\r\n`)
      },
    )

    it('prefixes a field starting with a carriage return, and quotes it — the CR is also a line-ending character', () => {
      const csv = toCSV(['Value'], [["\rcmd|'/bin/sh'!A1"]])

      expect(csv).toBe('Value\r\n"\'\rcmd|\'/bin/sh\'!A1"\r\n')
    })

    it('does not touch a field that merely contains one of the characters mid-string', () => {
      const csv = toCSV(['Value'], [['total-usage=12']])

      expect(csv).toBe('Value\r\ntotal-usage=12\r\n')
    })

    it('still quotes a neutralised field that also needs quoting', () => {
      const csv = toCSV(['Value'], [['=SUM(A1,A2)']])

      expect(csv).toBe('Value\r\n"\'=SUM(A1,A2)"\r\n')
    })

    it('applies to the header row too, since a generic table\'s columns are server-printed', () => {
      // A CRD author names its own kubectl columns, exactly as a label value
      // names itself — both reach this function as untrusted text.
      const csv = toCSV(['=HYPERLINK("http://evil")'], [['fine']])

      expect(csv).toBe('"\'=HYPERLINK(""http://evil"")"\r\nfine\r\n')
    })
  })
})
