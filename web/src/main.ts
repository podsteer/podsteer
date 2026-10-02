/**
 * Frontend entry point.
 *
 * Svelte 5 mounts imperatively via `mount()`; the `new App({ target })` form
 * from Svelte 4 is gone.
 */
import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { installGlobalErrorHandlers } from '$lib/globalErrors'
import { notices } from '$stores/notices.svelte'

const target = document.getElementById('app')
if (!target) {
  throw new Error('index.html is missing its #app mount point')
}

// Before the app mounts, so a rejection during start-up is caught too. Never
// removed: it is the page's lifetime.
installGlobalErrorHandlers({ report: (message) => notices.post(message) })

export default mount(App, { target })
