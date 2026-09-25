/**
 * The proxy PodSteer's own outbound calls go through.
 *
 * A STORE RATHER THAN COMPONENT STATE, for the reason the kubeconfig sources
 * have one: the value lives in the Go process's settings file, the pane is
 * mounted and unmounted with a dialog, and a control that re-read the world on
 * every mount would ask the backend a question it has already answered.
 *
 * WHAT IS NOT HERE IS VALIDATION. The domain refuses an unusable value on the
 * write path and returns the reason; repeating those rules in TypeScript would
 * mean two implementations of "is this an absolute http(s) URL" that agree
 * until the day they do not. What this holds is what came back.
 */
import { getProxy, setProxy, type ProxySettings } from '$lib/api/client'
import { toApiError } from '$lib/api/errors'

/** The three modes, in the order the pane offers them. */
export const PROXY_MODES = ['environment', 'none', 'manual'] as const
export type ProxyMode = (typeof PROXY_MODES)[number]

const DEFAULTS: ProxySettings = { mode: 'environment', url: '', noProxy: '' }

class ProxyStore {
  /** What the backend last said is in force. */
  current = $state<ProxySettings>({ ...DEFAULTS })
  status = $state<'idle' | 'loading' | 'ready' | 'saving' | 'error'>('idle')
  /** A refusal from the write path, in the domain's own words. */
  error = $state('')
  /** Set for a moment after a successful write, so the pane can confirm. */
  saved = $state(false)

  load = async (): Promise<void> => {
    if (this.status === 'loading') return
    this.status = 'loading'
    try {
      this.current = await getProxy()
      this.error = ''
      this.status = 'ready'
    } catch (cause) {
      this.error = toApiError(cause).message
      this.status = 'error'
    }
  }

  /**
   * Writes it, and only then updates what this store believes.
   *
   * THE OPTIMISTIC ORDER WOULD BE WRONG HERE. A proxy the backend refused —
   * a URL with a password in it, a scheme it will not dial — must not be left
   * on screen as though it were in force, because the operator's next move
   * depends on believing what the pane says about where their traffic goes.
   */
  save = async (mode: ProxyMode, url: string, noProxy: string): Promise<boolean> => {
    this.status = 'saving'
    this.saved = false
    try {
      await setProxy(mode, url.trim(), noProxy.trim())
      this.current = { mode, url: url.trim(), noProxy: noProxy.trim() }
      this.error = ''
      this.status = 'ready'
      this.saved = true
      return true
    } catch (cause) {
      this.error = toApiError(cause).message
      this.status = 'error'
      return false
    }
  }
}

export const proxy = new ProxyStore()
