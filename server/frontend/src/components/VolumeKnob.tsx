import { useRef, useCallback } from 'preact/hooks'

interface VolumeKnobProps {
  value: number // 0-100
  onChange: (value: number) => void
}

export function VolumeKnob({ value, onChange }: VolumeKnobProps) {
  const trackRef = useRef<HTMLDivElement>(null)
  const draggingRef = useRef(false)

  const clamp = (v: number) => Math.min(100, Math.max(0, v))

  const updateFromClientX = useCallback((clientX: number) => {
    const track = trackRef.current
    if (!track) return
    const rect = track.getBoundingClientRect()
    const pct = ((clientX - rect.left) / rect.width) * 100
    onChange(clamp(pct))
  }, [onChange])

  const handlePointerDown = useCallback((e: PointerEvent) => {
    e.preventDefault()
    const track = trackRef.current
    if (!track) return
    draggingRef.current = true
    track.setPointerCapture?.(e.pointerId)
    updateFromClientX(e.clientX)
  }, [updateFromClientX])

  const handlePointerMove = useCallback((e: PointerEvent) => {
    if (!draggingRef.current) return
    updateFromClientX(e.clientX)
  }, [updateFromClientX])

  const handlePointerUp = useCallback((e: PointerEvent) => {
    draggingRef.current = false
    const track = trackRef.current
    track?.releasePointerCapture?.(e.pointerId)
  }, [])

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    const largeStep = 10
    const smallStep = 5

    switch (e.key) {
      case 'ArrowLeft':
      case 'ArrowDown':
        e.preventDefault()
        onChange(clamp(value - smallStep))
        break
      case 'ArrowRight':
      case 'ArrowUp':
        e.preventDefault()
        onChange(clamp(value + smallStep))
        break
      case 'PageDown':
        e.preventDefault()
        onChange(clamp(value - largeStep))
        break
      case 'PageUp':
        e.preventDefault()
        onChange(clamp(value + largeStep))
        break
      case 'Home':
        e.preventDefault()
        onChange(0)
        break
      case 'End':
        e.preventDefault()
        onChange(100)
        break
      default:
        break
    }
  }, [onChange, value])

  return (
    <div class="flex items-center gap-3 w-full max-w-[180px]">
      {/* Volume low icon */}
      <svg class="w-4 h-4 text-text-tertiary flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polygon points="11 5 6 9 2 9 2 15 6 15 11 19" />
      </svg>

      {/* Slider track */}
      <div
        ref={trackRef}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
        onKeyDown={handleKeyDown}
        class="relative flex-1 h-8 flex items-center cursor-pointer group"
        role="slider"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={value}
        aria-valuetext={`${Math.round(value)}%`}
        aria-label="Volume"
        tabIndex={0}
      >
        {/* Track background */}
        <div class="absolute inset-x-0 h-1 rounded-full bg-[rgba(255,255,255,0.08)]">
          {/* Fill */}
          <div
            class="h-full rounded-full bg-accent transition-[width] duration-75"
            style={{ width: `${value}%` }}
          />
        </div>
        {/* Thumb */}
        <div
          class="absolute w-3.5 h-3.5 rounded-full bg-white shadow-[0_0_6px_rgba(0,0,0,0.4)] transition-[left] duration-75 group-hover:scale-110"
          style={{ left: `calc(${value}% - 7px)` }}
        />
      </div>

      {/* Volume high icon */}
      <svg class="w-4 h-4 text-text-tertiary flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polygon points="11 5 6 9 2 9 2 15 6 15 11 19" />
        <path d="M15.54 8.46a5 5 0 0 1 0 7.07" />
        <path d="M19.07 4.93a10 10 0 0 1 0 14.14" />
      </svg>
    </div>
  )
}
