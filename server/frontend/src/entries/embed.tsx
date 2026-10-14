import { applyBrandingTheme } from '../lib/brandingTheme'
import '../globals.css'
import { render } from 'preact'
import { Embed } from '@/pages/Embed'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<Embed />, document.getElementById('app')!)
