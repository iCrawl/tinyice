import { applyBrandingTheme } from '../lib/brandingTheme'
import { render } from 'preact'
import '../globals.css'
import { KioskDashboard } from '../pages/KioskDashboard'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<KioskDashboard />, document.getElementById('app')!)
