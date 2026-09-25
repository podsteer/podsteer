# Measurement harness

Not shipped, and not part of the application. Vite builds `index.html` alone,
so nothing here reaches a bundle; the licence scan reads `web/src` and never
looks in this directory.

## Why it exists

Vertical alignment of the list-row controls was got wrong three times in a
row, each time by reasoning about the box model and each time convincingly.
The third attempt was argued from the CSS specification, was correct about
every mechanism it named, and was still four pixels out — because the fault
was somewhere nobody had thought to look: a state layer larger than its own
control was sizing the grid track, so `place-items: center` was centring the
drawing inside the track rather than inside the control.

No amount of further reasoning would have found that. Measuring found it in
one call.

## Using it

```sh
npx vite --port 5199 --strictPort     # from web/
```

Then open `http://localhost:5199/dev/align.html` and read
`getBoundingClientRect()` off the parts you care about. What matters is not
any single number but whether the DRIFT between elements is identical: a row
whose contents are all 0.25px from the text baseline is aligned; one where a
single element differs is not, however plausible the reasoning behind it.

This runs the real components against the real stylesheet in a real engine.
It is Chromium rather than the WebKit the application ships in, so it settles
box-model questions and not engine-specific rendering — which is what this
class of fault has always turned out to be.
