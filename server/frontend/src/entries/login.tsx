import '../globals.css'
import { render } from 'preact'
import type { TinyIceBase } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'
import { Login } from '@/pages/Login'

const data = (window.__TINYICE__ ?? {}) as Partial<TinyIceBase>

applyBrandingTheme(data.branding?.accentColor)

render(<Login />, document.getElementById('app')!)
