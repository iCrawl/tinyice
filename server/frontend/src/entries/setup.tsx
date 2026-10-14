import { applyBrandingTheme } from '../lib/brandingTheme'
import '../globals.css'
import { render } from 'preact'
import { Setup } from '@/pages/Setup'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<Setup />, document.getElementById('app')!)
