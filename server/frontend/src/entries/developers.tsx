import { render } from 'preact'
import '../globals.css'
import type { TinyIceBase } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'
import { Developers } from '../pages/Developers'

const data = (window.__TINYICE__ ?? {}) as Partial<TinyIceBase>

applyBrandingTheme(data.branding?.accentColor)

render(<Developers />, document.getElementById('app')!)
