import * as XLSX from 'xlsx'

export const downloadTextFile = ({
  filename,
  content,
  mimeType,
}: {
  filename: string
  content: string
  mimeType: string
}) => {
  const blob = new Blob([content], { type: mimeType })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

const escapeCell = (value: string | number | boolean | null | undefined) => {
  const text = value === null || value === undefined ? '' : String(value)
  const escaped = text.replaceAll('"', '""')
  return `"${escaped}"`
}

export const downloadCsv = ({
  filename,
  headers,
  rows,
}: {
  filename: string
  headers: string[]
  rows: Array<Array<string | number | boolean | null | undefined>>
}) => {
  const content = [
    headers.map((header) => escapeCell(header)).join(','),
    ...rows.map((row) => row.map((cell) => escapeCell(cell)).join(',')),
  ].join('\n')

  downloadTextFile({
    filename,
    content,
    mimeType: 'text/csv;charset=utf-8',
  })
}

export const downloadWorkbook = ({
  filename,
  sheets,
}: {
  filename: string
  sheets: Array<{
    name: string
    rows: Array<Record<string, string | number | boolean | null | undefined>>
  }>
}) => {
  const workbook = XLSX.utils.book_new()

  sheets.forEach((sheet) => {
    const worksheet = XLSX.utils.json_to_sheet(sheet.rows)
    XLSX.utils.book_append_sheet(workbook, worksheet, sheet.name.slice(0, 31))
  })

  XLSX.writeFile(workbook, filename)
}
