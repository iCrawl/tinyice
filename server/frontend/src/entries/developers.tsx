import { applyBrandingTheme } from '../lib/brandingTheme'
import { render } from 'preact'
import '../globals.css'
import { Developers } from '../pages/Developers'
applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<Developers />, document.getElementById('app')!)
