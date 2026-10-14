import { applyBrandingTheme } from '../lib/brandingTheme'
import { render } from 'preact'
import '../globals.css'
import { AdminLayout } from '../pages/admin/AdminLayout'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<AdminLayout />, document.getElementById('app')!)
