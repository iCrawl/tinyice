import '../globals.css'
import { render } from 'preact'
import type { PlayerData } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'
import { Embed } from '@/pages/Embed'

const data = (window.__TINYICE__ ?? {}) as Partial<PlayerData>

applyBrandingTheme(data.branding?.accentColor)

render(<Embed />, document.getElementById('app')!)
