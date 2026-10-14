import { applyBrandingTheme } from '../lib/brandingTheme'
import '../globals.css'
import { render } from 'preact'
import { Explore } from '@/pages/Explore'

applyBrandingTheme(window.__TINYICE__?.branding?.accentColor)

render(<Explore />, document.getElementById('app')!)
