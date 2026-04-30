import '../globals.css'
import { render } from 'preact'
import type { LandingData } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'
import { Explore } from '@/pages/Explore'

const data = (window.__TINYICE__ ?? {}) as Partial<LandingData>

applyBrandingTheme(data.branding?.accentColor)

render(<Explore />, document.getElementById('app')!)
