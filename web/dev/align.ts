// A harness that exists only to MEASURE. Not shipped: `dev/` is outside the
// Vite root's src tree and is never imported by the application.
import { mount } from 'svelte'

import '../src/app.css'
import Harness from './AlignHarness.svelte'

mount(Harness, { target: document.getElementById('app')! })
