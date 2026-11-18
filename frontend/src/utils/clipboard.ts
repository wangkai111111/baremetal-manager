// Clipboard API requires HTTPS; retain a user-initiated fallback for LAN HTTP.
export async function copyToClipboard(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      return
    } catch {
      // Browser permissions may block the API even in a secure context.
    }
  }

  const focused = document.activeElement
  const selection = document.getSelection()
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange())
    : []
  const input = document.createElement('textarea')
  input.value = value
  input.readOnly = true
  input.style.cssText = 'position:fixed;left:-9999px;top:0;opacity:0'
  // Keep the fallback within the active modal's focus trap when applicable.
  const container = focused?.closest('[role="dialog"]') || document.body
  container.appendChild(input)
  try {
    input.focus({ preventScroll: true })
    input.select()
    if (!document.execCommand('copy')) throw new Error('Clipboard copy was rejected')
  } finally {
    input.remove()
    if (focused instanceof HTMLElement && focused.isConnected) focused.focus({ preventScroll: true })
    if (selection) {
      selection.removeAllRanges()
      for (const range of ranges) selection.addRange(range)
    }
  }
}
