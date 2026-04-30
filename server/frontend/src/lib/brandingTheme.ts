const DEFAULT_ACCENT_COLOR = '#ff6600'

function normalizeHexColor(color?: string | null): string {
  const trimmed = color?.trim()
  if (!trimmed) return DEFAULT_ACCENT_COLOR

  const hex = trimmed.startsWith('#') ? trimmed.slice(1) : trimmed
  if (!/^[\da-fA-F]{3}$|^[\da-fA-F]{6}$/.test(hex)) {
    return DEFAULT_ACCENT_COLOR
  }

  const expanded = hex.length === 3
    ? hex.split('').map((char) => char + char).join('')
    : hex

  return `#${expanded.toLowerCase()}`
}

function hexToRgb(hexColor: string): [number, number, number] {
  const color = normalizeHexColor(hexColor).slice(1)
  return [
    parseInt(color.slice(0, 2), 16),
    parseInt(color.slice(2, 4), 16),
    parseInt(color.slice(4, 6), 16),
  ]
}

export function applyBrandingTheme(accentColor?: string | null, root: HTMLElement = document.documentElement) {
  const normalizedColor = normalizeHexColor(accentColor)
  const [red, green, blue] = hexToRgb(normalizedColor)

  root.style.setProperty('--accent-override', normalizedColor)
  root.style.setProperty('--accent-override-rgb', `${red}, ${green}, ${blue}`)
  root.style.setProperty('--color-accent-subtle', `rgba(${red}, ${green}, ${blue}, 0.08)`)
  root.style.setProperty('--color-accent-glow', `rgba(${red}, ${green}, ${blue}, 0.15)`)
  root.style.setProperty('--color-border-accent', `rgba(${red}, ${green}, ${blue}, 0.20)`)
}
