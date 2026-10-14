import { applyBrandingTheme } from '../lib/brandingTheme'
import '../globals.css'
import { render } from 'preact'
import { Landing } from '@/pages/Landing'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<Landing />, document.getElementById('app')!)
