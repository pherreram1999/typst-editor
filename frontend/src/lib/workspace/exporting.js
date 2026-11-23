// Exportar un proyecto: PDF compilado (descarga o menú «Compartir» del
// sistema) y .zip de las fuentes. Lo usan ShareModal y el menú del celular.
import { api } from '../api.js'

// Descarga un archivo con el gestor de descargas del navegador.
export function download(href, filename) {
  const a = document.createElement('a')
  a.href = href
  if (filename) a.download = filename
  document.body.append(a)
  a.click()
  a.remove()
}

export const pdfFilename = (project) => `${project.name.replace(/[\\/:*?"<>|]+/g, '-').trim() || 'documento'}.pdf`

// Mensaje para un error al pedir el PDF (el documento no compila).
export function pdfError(e) {
  return e.data?.diagnostics ? 'El documento tiene errores y no se pudo generar el PDF. Revísalos en «Problemas».' : e.message
}

export async function downloadPdf(project) {
  const { blob } = await api.pdf(project.id)
  const href = URL.createObjectURL(blob)
  download(href, pdfFilename(project))
  setTimeout(() => URL.revokeObjectURL(href), 60000)
}

// El navegador puede pasar un archivo al menú «Compartir» del sistema
// (WhatsApp, correo, Drive…): sobre todo en celulares.
export const canSharePdf =
  typeof navigator !== 'undefined' &&
  typeof navigator.canShare === 'function' &&
  typeof File === 'function' &&
  (() => {
    try {
      return navigator.canShare({ files: [new File([''], 'x.pdf', { type: 'application/pdf' })] })
    } catch {
      return false
    }
  })()

// Devuelve false si la persona canceló el menú del sistema.
export async function sharePdf(project) {
  const { blob } = await api.pdf(project.id)
  const file = new File([blob], pdfFilename(project), { type: 'application/pdf' })
  try {
    await navigator.share({ files: [file], title: project.name })
    return true
  } catch (e) {
    if (e.name === 'AbortError') return false
    throw e
  }
}

// El servidor arma el .zip mientras lo envía: la descarga sigue en el navegador.
export async function downloadZip(project) {
  download(await api.exportZipURL(project.id))
}
