import '../globals.css'
import { render } from 'preact'
import type { LandingData } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'
import { Landing } from '@/pages/Landing'

const data = (window.__TINYICE__ ?? {}) as Partial<LandingData>

applyBrandingTheme(data.branding?.accentColor)

render(<Landing />, document.getElementById('app')!)
