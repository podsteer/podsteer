<!--
  "Can I…" — one access review, from the Permissions page's toolbar.

  THE API SERVER DECIDES AND THIS ONLY QUOTES. What comes back is the review's
  own verdict and reason, rendered as they arrived; nothing here evaluates a
  rule. Why that matters, and what asking about somebody else costs, is under
  the (?) as the `can-i` topic — the dialog carries the question and the
  answer, not the explanation.

  The fields survive closing and reopening, because the usual next question is
  the last one with one word changed. The answer does not survive a change of
  cluster: an allow shown under another tab's name is a claim about the wrong
  cluster.
-->
<script lang="ts">
  import { escapeLayer, type EscapeClaim } from '$lib/escape'
  import { modal } from '$lib/modal'
  import { toApiError, type ApiError } from '$lib/api/errors'
  import { canI as askCanI, type AccessDecision } from '$lib/api/client'
  import { describeDecision, reviewState } from '$lib/rbac'
  import { authCanI } from '$lib/kubectl'
  import Button from './Button.svelte'
  import DialogFooter from './DialogFooter.svelte'
  import DialogHeader from './DialogHeader.svelte'
  import ErrorBanner from './ErrorBanner.svelte'
  import ReviewNotice from './ReviewNotice.svelte'
  import Select from './Select.svelte'
  import { Check, ShieldQuestion, X } from '@lucide/svelte'

  interface Props {
    open: boolean
    clusterId: string
    /** The tab's namespace, offered as the question's; '' when on every one. */
    namespace: string
    onclose: () => void
  }

  let { open, clusterId, namespace, onclose }: Props = $props()

  const SUBJECT_KINDS = [
    { value: '', label: 'This account' },
    { value: 'User', label: 'User' },
    { value: 'Group', label: 'Group' },
    { value: 'ServiceAccount', label: 'ServiceAccount' },
  ]

  let subjectKind = $state('')
  let subjectName = $state('')
  let subjectNamespace = $state('')
  let verb = $state('get')
  let group = $state('')
  let resource = $state('pods')
  let subresource = $state('')
  let questionNamespace = $state('')
  let objectName = $state('')

  let decision = $state<AccessDecision | null>(null)
  let decisionLoading = $state(false)
  let decisionError = $state<ApiError | null>(null)

  /** Which cluster the answer on screen is about. */
  let answeredFor = ''

  // The tab's namespace is the likely question, so it is offered on the first
  // opening; after that the operator's own edits stand.
  let seeded = false
  $effect(() => {
    if (!open || seeded) return
    seeded = true
    questionNamespace = namespace
  })

  $effect(() => {
    if (clusterId !== answeredFor) {
      decision = null
      decisionError = null
    }
  })

  const canAsk = $derived(
    verb.trim() !== '' && resource.trim() !== '' && (subjectKind === '' || subjectName.trim() !== ''),
  )

  const question = $derived({
    subjectKind,
    subjectName: subjectKind === '' ? '' : subjectName.trim(),
    subjectNamespace: subjectKind === 'ServiceAccount' ? subjectNamespace.trim() : '',
    verb: verb.trim(),
    group: group.trim(),
    resource: resource.trim(),
    subresource: subresource.trim(),
    namespace: questionNamespace.trim(),
    name: objectName.trim(),
  })

  const command = $derived(canAsk ? authCanI(clusterId, question) : '')

  async function ask(): Promise<void> {
    if (!canAsk || decisionLoading) return
    decisionLoading = true
    decisionError = null
    answeredFor = clusterId
    try {
      decision = await askCanI(clusterId, question)
    } catch (cause) {
      decision = null
      decisionError = toApiError(cause)
    } finally {
      decisionLoading = false
    }
  }

  const decisionState = $derived(decision ? reviewState(decision.status, decision.refusal) : null)
  const summary = $derived(decision ? describeDecision(decision) : null)

  function onKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== 'Escape') return
    if (!escape?.owns()) return
    onclose()
  }

  let escape = $state<EscapeClaim | null>(null)
  $effect(() => {
    if (!open) return
    const held = escapeLayer()
    escape = held
    return () => {
      held.release()
      escape = null
    }
  })
</script>

<svelte:window onkeydown={onKeydown} />

{#snippet field(label: string, placeholder: string, value: string, set: (v: string) => void)}
  <label class="flex flex-col gap-1">
    <span class="text-label-medium text-on-surface-variant">{label}</span>
    <input
      type="text"
      {value}
      oninput={(event) => set(event.currentTarget.value)}
      autocomplete="off"
      spellcheck="false"
      {placeholder}
      class="field w-full px-3 py-2 text-body-medium"
    />
  </label>
{/snippet}

{#if open}
  <button
    type="button"
    aria-label="Close dialog"
    tabindex="-1"
    class="fixed inset-0 z-[60] cursor-default bg-scrim/40"
    onclick={onclose}
  ></button>

  <div
    class="fixed inset-0 z-[70] m-auto h-fit max-h-[90vh] overflow-y-auto
           w-[40rem] max-w-[92vw]
           rounded-sm border border-outline-variant bg-surface-container-high p-6 shadow-level-3"
    role="dialog"
    aria-modal="true"
    use:modal
    aria-label="Can I"
  >
    <DialogHeader title="Can I…" icon={ShieldQuestion} help="can-i" {onclose} />

    <!-- A form, so Enter in any field asks. -->
    <form
      class="mt-5"
      onsubmit={(event) => {
        event.preventDefault()
        void ask()
      }}
    >
      <div class="grid gap-3 sm:grid-cols-3">
        {@render field('Verb', 'get', verb, (v) => (verb = v))}
        {@render field('Resource', 'pods', resource, (v) => (resource = v))}
        {@render field('API group', '(core)', group, (v) => (group = v))}
        {@render field('Subresource', 'log, exec…', subresource, (v) => (subresource = v))}
        {@render field('Namespace', '(cluster scope)', questionNamespace, (v) => (questionNamespace = v))}
        {@render field('Object name', '(any)', objectName, (v) => (objectName = v))}
      </div>

      <div class="mt-3 grid gap-3 sm:grid-cols-3">
        <div class="flex flex-col gap-1">
          <span class="text-label-medium text-on-surface-variant" aria-hidden="true">Subject</span>
          <Select
            label="Subject"
            value={subjectKind}
            options={SUBJECT_KINDS}
            onchange={(value) => (subjectKind = value)}
            class="w-full"
          />
        </div>
        {#if subjectKind !== ''}
          {@render field('Subject name', '', subjectName, (v) => (subjectName = v))}
        {/if}
        {#if subjectKind === 'ServiceAccount'}
          {@render field('Subject namespace', '', subjectNamespace, (v) => (subjectNamespace = v))}
        {/if}
      </div>

      <ErrorBanner error={decisionError} ondismiss={() => (decisionError = null)} class="mt-4" />

      {#if decisionState?.kind === 'unavailable'}
        <div class="mt-4"><ReviewNotice state={decisionState} /></div>
      {:else if decision && summary}
        <div
          class="mt-4 flex items-start gap-2.5 rounded-sm px-3 py-2.5
                 {summary.tone === 'allowed'
            ? 'bg-success-container/40 text-on-surface'
            : summary.tone === 'denied'
              ? 'bg-error-container/40 text-on-surface'
              : 'bg-surface-container text-on-surface'}"
          role="status"
        >
          {#if summary.tone === 'allowed'}
            <Check class="mt-0.5 size-4 shrink-0" strokeWidth={2.2} />
          {:else if summary.tone === 'denied'}
            <X class="mt-0.5 size-4 shrink-0" strokeWidth={2.2} />
          {:else}
            <ShieldQuestion class="mt-0.5 size-4 shrink-0" strokeWidth={2} />
          {/if}
          <div class="min-w-0">
            <p class="text-body-medium font-medium">{summary.label}</p>
            <p class="mt-0.5 font-mono text-body-small text-on-surface-variant" data-selectable>
              {decision.request.verb}
              {decision.request.group ? `${decision.request.group}/` : ''}{decision.request.resource}{decision.request
                .subresource
                ? `/${decision.request.subresource}`
                : ''}
              {decision.request.namespace ? `in ${decision.request.namespace}` : '(cluster scope)'}
              {decision.request.name ? `named ${decision.request.name}` : ''}
            </p>
            {#if summary.reason}
              <!-- Verbatim. The reason names the binding that decided it,
                   which is the whole value of showing it. -->
              <p class="mt-1 text-body-medium text-on-surface-variant" data-selectable>{summary.reason}</p>
            {/if}
            {#if summary.evaluationError}
              <p class="mt-1 text-body-medium text-on-surface-variant">
                The authorizer reported a problem, so this answer may be incomplete:
                {summary.evaluationError}
              </p>
            {/if}
          </div>
        </div>
      {/if}

      <DialogFooter {command} label="kubectl equivalent">
        <Button variant="outlined" onclick={onclose}>Close</Button>
        <Button type="submit" loading={decisionLoading} disabled={!canAsk}>Ask the cluster</Button>
      </DialogFooter>
    </form>
  </div>
{/if}
