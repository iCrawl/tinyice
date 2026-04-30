import '../globals.css'
import { render } from 'preact'
import type { TinyIceBase } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'
import { Setup } from '@/pages/Setup'

const data = (window.__TINYICE__ ?? {}) as Partial<TinyIceBase>

applyBrandingTheme(data.branding?.accentColor)

render(<Setup />, document.getElementById('app')!)
