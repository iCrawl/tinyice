import { applyBrandingTheme } from '../lib/brandingTheme'
import '../globals.css'
import { render } from 'preact'
import { Login } from '@/pages/Login'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<Login />, document.getElementById('app')!)
