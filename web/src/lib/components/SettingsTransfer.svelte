<!--
  Export and import the local organisation as a file.

  IMPORT IS A REVIEW, NEVER A SILENT OVERWRITE. Choosing a file parses it and
  shows what will change, what will be added and what will be left alone;
  nothing is written until Apply. The review is a diff of the exact state the
  apply will set — see `previewImport` — so the two cannot disagree, which is
  the rule BulkActionDialog follows for the same reason.

  Everything about what the file carries, and what it must never carry, is in
  `$lib/settingsFile`. The short version is on screen below, because somebody
  about to send this to a colleague deserves to read it before they do rather
  than after.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { readTextFile, saveTextFile } from '$lib/api/client'
  import { toApiError } from '$lib/api/errors'
  import {
    applyImport,
    buildDocument,
    buildSettingsFilename,
    countChanges,
    currentPayload,
    parseDocument,
    previewImport,
    serialiseDocument,
    type ImportMode,
    type ImportOutcome,
    type ImportPreview,
  } from '$lib/settingsFile'
  import { workspace } from '$stores/workspace.svelte'
  import Button from './Button.svelte'
  import { CircleCheck, CirclePlus, CircleMinus, Download, Minus, Upload } from '@lucide/svelte'

  /** What was read from disk, kept so the mode can be changed without re-reading. */
  let parsed = $state<ReturnType<typeof parseDocument> | null>(null)
  let mode = $state<ImportMode>('merge')

  let exporting = $state(false)
  let importing = $state(false)
  let applied = $state(false)
  /** The last thing that went wrong, or where the export landed. */
  let notice = $state<{ tone: 'ok' | 'bad'; text: string } | null>(null)

  /**
   * The review, re-derived whenever the mode changes.
   *
   * `currentPayload()` is read here rather than captured when the file was
   * chosen, so switching Merge to Replace re-diffs against what is actually
   * loaded — and so does a review left open while something else changed a
   * setting behind it.
   */
  const preview = $derived<ImportPreview | null>(
    parsed?.ok ? previewImport(currentPayload(), parsed.document, mode) : null,
  )

  const counts = $derived(preview ? countChanges(preview.entries) : null)

  /** The lines worth reading first: everything that is not left alone. */
  const changing = $derived(preview ? preview.entries.filter((e) => e.outcome !== 'same') : [])
  const unchanged = $derived(preview ? preview.entries.filter((e) => e.outcome === 'same') : [])

  let showUnchanged = $state(false)

  const OUTCOME_META: Record<ImportOutcome, { label: string; icon: typeof CirclePlus; tone: string }> =
    {
      add: { label: 'Added', icon: CirclePlus, tone: 'text-success' },
      change: { label: 'Changed', icon: CircleCheck, tone: 'text-primary' },
      remove: { label: 'Removed', icon: CircleMinus, tone: 'text-error' },
      same: { label: 'Left alone', icon: Minus, tone: 'text-on-surface-variant/60' },
    }

  async function exportSettings(): Promise<void> {
    exporting = true
    notice = null
    try {
      const text = serialiseDocument(buildDocument())
      const path = await saveTextFile(buildSettingsFilename(), text)
      // "" is a cancelled dialog, which is not a failure and not worth a
      // message either — the operator knows they cancelled.
      if (path) notice = { tone: 'ok', text: `Saved to ${path}` }
    } catch (error) {
      notice = { tone: 'bad', text: toApiError(error).message }
    } finally {
      exporting = false
    }
  }

  async function chooseFile(): Promise<void> {
    importing = true
    notice = null
    applied = false
    parsed = null
    try {
      const text = await readTextFile('Choose a PodSteer settings file')
      if (!text) return
      const result = parseDocument(text)
      if (!result.ok) {
        // Refused whole, never partly applied: nothing has been written at
        // this point and nothing will be.
        notice = { tone: 'bad', text: result.reason }
        return
      }
      parsed = result
    } catch (error) {
      notice = { tone: 'bad', text: toApiError(error).message }
    } finally {
      importing = false
    }
  }

  function apply(): void {
    if (!preview) return
    applyImport(preview)

    // An import can change which group a cluster is in, or that group's
    // read-only flag, for every open tab at once — which is exactly what
    // syncAllReadOnly exists for (see workspace.svelte.ts). Skipping it would
    // leave the backend enforcing the guard the PREVIOUS arrangement set,
    // while the UI showed the new one.
    workspace.syncAllReadOnly()

    applied = true
    parsed = null
    notice = { tone: 'ok', text: 'Settings imported.' }
  }

  function discard(): void {
    parsed = null
    notice = null
  }

  /**
   * Escape discards a pending review before it closes Settings.
   *
   * One Escape, one layer — see $lib/escape. Without the claim, a keystroke
   * aimed at "never mind, I picked the wrong file" would close the whole
   * dialog instead, which reads as the import having happened.
   */
  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!parsed?.ok) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !parsed?.ok) return
    if (!escape?.owns()) return
    event.preventDefault()
    discard()
  }

  /** A timestamp as written, and verbatim when it is not one we can read. */
  function exportedWhen(raw: string): string {
    const at = new Date(raw)
    return Number.isNaN(at.getTime()) ? raw : at.toLocaleString()
  }
</script>

<svelte:window onkeydown={onKeydown} />

<section class="flex flex-col gap-6">
  <div>
    <h3 class="text-title-medium text-on-surface">Export &amp; import</h3>
    <p class="mt-0.5 text-body-small leading-relaxed text-on-surface-variant">
      Everything you have arranged on this machine as one readable file: your projects, groups and
      their guardrails, pinned kinds, column layouts and custom columns, thresholds, refresh and
      appearance, the remembered port-forward ports, and the debug and node-shell defaults. Keep it
      in git, or hand it to a colleague so a new machine looks like this one.
    </p>

    <!--
      SAID BEFORE THE BUTTON, not after. The whole risk of this feature is
      somebody sharing more than they meant to, and a warning that arrives
      once the file is on disk has arrived too late.
    -->
    <p
      class="mt-3 rounded-sm border border-outline-variant/50 bg-surface-container px-3 py-2
             text-body-small leading-relaxed text-on-surface-variant"
    >
      It carries <span class="text-on-surface">no credentials</span>, no kubeconfig, no cluster
      addresses and <span class="text-on-surface">no object names</span> — no pod, node, namespace
      or workload appears in it. It does carry your kubeconfig
      <span class="text-on-surface">context names</span>, because a group cannot be attached to a
      cluster without naming one; anyone you send the file to will see which contexts you have. The
      file says so in its own header.
    </p>
  </div>

  <div class="flex flex-wrap items-center gap-3">
    <Button variant="tonal" loading={exporting} onclick={() => void exportSettings()}>
      <span class="flex items-center gap-2"><Download class="size-4" strokeWidth={2} /> Export</span>
    </Button>
    <Button variant="outlined" loading={importing} onclick={() => void chooseFile()}>
      <span class="flex items-center gap-2"><Upload class="size-4" strokeWidth={2} /> Import…</span>
    </Button>

    {#if notice}
      <span
        class="text-body-small {notice.tone === 'bad' ? 'text-error' : 'text-on-surface-variant'}"
        role={notice.tone === 'bad' ? 'alert' : 'status'}
      >
        {notice.text}
      </span>
    {/if}
  </div>

  {#if preview && counts}
    <div class="rounded-sm border border-outline-variant">
      <div class="border-b border-outline-variant/60 px-4 py-3">
        <h4 class="text-title-small text-on-surface">Review before importing</h4>
        <p class="mt-0.5 text-body-small text-on-surface-variant">
          Written {exportedWhen(preview.exportedAt)} · settings version {preview.version}
        </p>

        <!--
          A newer build's file is a fact, not a fault. It is stated rather than
          refused, and the count says how much of it this build could not read
          — which is the honest answer to "did all of it arrive".
        -->
        {#if preview.fromTheFuture}
          <p class="mt-2 text-body-small text-warning">
            This file was written by a newer PodSteer (settings version {preview.version}); this
            build understands version 1. Everything it recognises is listed below; anything else is
            ignored.
          </p>
        {/if}

        {#if preview.unknownFields > 0}
          <p class="mt-2 text-body-small text-on-surface-variant">
            {preview.unknownFields}
            {preview.unknownFields === 1 ? 'setting' : 'settings'} in the file
            {preview.unknownFields === 1 ? 'is' : 'are'} not known to this build and will be ignored.
          </p>
        {/if}
        {#if preview.invalidFields > 0}
          <p class="mt-2 text-body-small text-warning">
            {preview.invalidFields}
            {preview.invalidFields === 1 ? 'setting' : 'settings'} in the file
            {preview.invalidFields === 1 ? 'holds a value' : 'hold values'} PodSteer will not accept, and
            {preview.invalidFields === 1 ? 'was' : 'were'} dropped.
          </p>
        {/if}

        <!-- Merge first, and the default: combining two people's arrangements
             is the ordinary reason to import one, and replace is the one that
             throws something away. -->
        <div class="mt-3 flex">
          {#each [{ id: 'merge', label: 'Merge' }, { id: 'replace', label: 'Replace' }] as choice (choice.id)}
            <button
              type="button"
              onclick={() => (mode = choice.id as ImportMode)}
              aria-pressed={mode === choice.id}
              class="state-layer h-9 min-w-28 border text-label-large transition-colors
                     duration-150 ease-standard
                     {choice.id === 'merge' ? 'rounded-l-xs' : '-ml-px rounded-r-xs'}
                     {mode === choice.id
                       ? 'border-transparent bg-secondary-container text-on-secondary-container'
                       : 'border-outline text-on-surface-variant'}"
            >
              {choice.label}
            </button>
          {/each}
        </div>
        <p class="mt-1.5 text-body-small text-on-surface-variant/80">
          {#if mode === 'merge'}
            Keeps everything this file does not mention, including projects and groups only this
            machine has.
          {:else}
            Makes these settings exactly what the file says. Projects, groups and layouts only this
            machine has are removed. Nothing outside the file's scope is touched.
          {/if}
        </p>

        <p class="mt-3 text-body-small text-on-surface">
          {counts.change} changed · {counts.add} added · {counts.remove} removed · {counts.same}
          left alone
        </p>
      </div>

      <ul class="max-h-64 overflow-y-auto">
        {#each changing as entry, index (`${entry.section}-${entry.label}-${index}`)}
          {@const meta = OUTCOME_META[entry.outcome]}
          {@const Icon = meta.icon}
          <li class="flex items-start gap-3 border-b border-outline-variant/40 px-4 py-2 last:border-b-0">
            <Icon class="mt-0.5 size-4 shrink-0 {meta.tone}" strokeWidth={2} />
            <div class="min-w-0 flex-1">
              <p class="text-body-medium text-on-surface">
                {entry.label}
                <span class="ml-1 text-label-small text-on-surface-variant">{entry.section}</span>
              </p>
              <p class="text-body-small break-words text-on-surface-variant">
                {#if entry.outcome === 'add'}
                  {entry.to}
                {:else if entry.outcome === 'remove'}
                  {entry.from}
                {:else}
                  {entry.from} → {entry.to}
                {/if}
              </p>
            </div>
            <span class="shrink-0 text-label-small {meta.tone}">{meta.label}</span>
          </li>
        {/each}

        {#if changing.length === 0}
          <li class="px-4 py-3 text-body-small text-on-surface-variant">
            Nothing would change — this file matches what is already set.
          </li>
        {/if}
      </ul>

      {#if unchanged.length > 0}
        <div class="border-t border-outline-variant/60">
          <button
            type="button"
            onclick={() => (showUnchanged = !showUnchanged)}
            aria-expanded={showUnchanged}
            class="state-layer w-full px-4 py-2 text-left text-body-small text-on-surface-variant
                   transition-colors duration-100 hover:text-on-surface"
          >
            {showUnchanged ? 'Hide' : 'Show'} the {unchanged.length} settings left alone
          </button>

          {#if showUnchanged}
            <ul class="max-h-48 overflow-y-auto border-t border-outline-variant/40">
              {#each unchanged as entry, index (`${entry.section}-${entry.label}-${index}`)}
                <li class="flex items-start gap-3 px-4 py-1.5">
                  <span class="min-w-0 flex-1 text-body-small text-on-surface-variant">
                    {entry.label}
                  </span>
                  <span class="shrink-0 text-body-small text-on-surface-variant/60">{entry.to}</span>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}

      <div class="flex justify-end gap-2 border-t border-outline-variant/60 px-4 py-3">
        <Button variant="text" onclick={discard}>Cancel</Button>
        <Button variant="filled" onclick={apply}>
          {mode === 'merge' ? 'Merge these settings' : 'Replace these settings'}
        </Button>
      </div>
    </div>
  {/if}

  {#if applied}
    <p class="text-body-small text-on-surface-variant">
      Everything is applied: the theme, the navigator and the column layouts changed as you
      watched, and every open cluster's read-only guard has been re-sent to the backend to match
      its new group.
    </p>
  {/if}
</section>
