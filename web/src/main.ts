/**
 * Frontend entry point.
 *
 * Svelte 5 mounts imperatively via `mount()`; the `new App({ target })` form
 * from Svelte 4 is gone.
 */
import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'

const target = document.getElementById('app')
if (!target) {
  throw new Error('index.html is missing its #app mount point')
}

export default mount(App, { target })
