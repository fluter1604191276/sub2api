import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  buildEmbeddedUrl,
  detectTheme,
  isSafeRelativePath,
  normalizeInternalAppPath,
} from '../embedded-url'

describe('embedded-url', () => {
  const originalLocation = window.location

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: {
        origin: 'https://app.example.com',
        href: 'https://app.example.com/user/purchase',
      },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true,
    })
    document.documentElement.classList.remove('dark')
    vi.restoreAllMocks()
  })

  it('adds embedded query parameters including locale and source context', () => {
    const result = buildEmbeddedUrl(
      'https://pay.example.com/checkout?plan=pro',
      42,
      'token-123',
      'dark',
      'zh-CN',
    )

    const url = new URL(result)
    expect(url.searchParams.get('plan')).toBe('pro')
    expect(url.searchParams.get('user_id')).toBe('42')
    expect(url.searchParams.get('token')).toBe('token-123')
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('lang')).toBe('zh-CN')
    expect(url.searchParams.get('ui_mode')).toBe('embedded')
    expect(url.searchParams.get('src_host')).toBe('https://app.example.com')
    expect(url.searchParams.get('src_url')).toBe('https://app.example.com/user/purchase')
  })

  it('omits optional params when they are empty', () => {
    const result = buildEmbeddedUrl('https://pay.example.com/checkout', undefined, '', 'light')

    const url = new URL(result)
    expect(url.searchParams.get('theme')).toBe('light')
    expect(url.searchParams.get('ui_mode')).toBe('embedded')
    expect(url.searchParams.has('user_id')).toBe(false)
    expect(url.searchParams.has('token')).toBe(false)
    expect(url.searchParams.has('lang')).toBe(false)
  })

  it('supports relative same-origin iframe paths without leaking auth token', () => {
    const result = buildEmbeddedUrl('/model-directory?source=menu', 42, 'token-123', 'dark', 'zh-CN')

    const url = new URL(result, window.location.origin)
    expect(result.startsWith('/model-directory?')).toBe(true)
    expect(url.searchParams.get('source')).toBe('menu')
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('lang')).toBe('zh-CN')
    expect(url.searchParams.get('ui_mode')).toBe('embedded')
    expect(url.searchParams.has('user_id')).toBe(false)
    expect(url.searchParams.has('token')).toBe(false)
  })

  it('returns original string for invalid url input', () => {
    expect(buildEmbeddedUrl('not a url', 1, 'token')).toBe('not a url')
  })

  it('rejects protocol-relative and backslash paths as safe relative paths', () => {
    expect(isSafeRelativePath('/model-directory')).toBe(true)
    expect(isSafeRelativePath('//evil.example')).toBe(false)
    expect(isSafeRelativePath('/\\evil')).toBe(false)
  })

  it('normalizes internal app model directory routes', () => {
    expect(normalizeInternalAppPath('/model-directory?source=menu#top')).toBe(
      '/available-channels?source=menu#top',
    )
    expect(normalizeInternalAppPath('https://app.example.com/available-channels')).toBe(
      '/available-channels',
    )
    expect(normalizeInternalAppPath('https://pay.example.com/available-channels')).toBe('')
    expect(normalizeInternalAppPath('md:fluterapi-guide')).toBe('')
  })

  it('detects dark mode from document root class', () => {
    document.documentElement.classList.add('dark')
    expect(detectTheme()).toBe('dark')
  })
})
