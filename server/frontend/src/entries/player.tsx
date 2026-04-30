import { render } from 'preact'
import '../globals.css'
import type { PlayerData } from '../types'
import { applyBrandingTheme } from '../lib/brandingTheme'
import { Player } from '@/pages/Player'

const data = (window.__TINYICE__ ?? {}) as Partial<PlayerData>

applyBrandingTheme(data.branding?.accentColor)

render(<Player />, document.getElementById('app')!)
