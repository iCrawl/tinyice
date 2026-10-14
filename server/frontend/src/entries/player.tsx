import { applyBrandingTheme } from '../lib/brandingTheme'
import { render } from 'preact'
import '../globals.css'
import { Player } from '@/pages/Player'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<Player />, document.getElementById('app')!)
