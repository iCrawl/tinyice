import { render } from 'preact'
import '../globals.css'
import { AdminLayout } from '../pages/admin/AdminLayout'
import type { AdminData } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'

const data = (window.__TINYICE__ ?? {}) as Partial<AdminData>

applyBrandingTheme(data.branding?.accentColor)

render(<AdminLayout />, document.getElementById('app')!)
