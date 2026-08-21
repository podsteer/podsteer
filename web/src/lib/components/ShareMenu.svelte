<!--
  "Share on…" — a menu of pre-filled share links for PodSteer itself, distinct
  from the GitHub/LinkedIn/Bluesky icons beside it in the status bar, which
  link to PodSteer's own accounts to *follow*. This is for spreading the word:
  an operator who likes the app sharing it with their own audience.

  Opens upward (`bottom-full`), because this lives in the footer — there is
  no room below it for a dropdown to open into.
-->
<script lang="ts">
  import type { Component } from 'svelte'
  import { openURL } from '$lib/api/client'
  import { Share2, Mail, Copy, Check } from '@lucide/svelte'
  import XIcon from './icons/XIcon.svelte'
  import LinkedinIcon from './icons/LinkedinIcon.svelte'
  import BlueskyIcon from './icons/BlueskyIcon.svelte'
  import RedditIcon from './icons/RedditIcon.svelte'
  import HackerNewsIcon from './icons/HackerNewsIcon.svelte'

  /** What gets shared, everywhere — one place to change the pitch. */
  const SHARE_URL = 'https://podsteer.com'
  const SHARE_TEXT = 'PodSteer — a fast, native Kubernetes desktop client.'

  interface ShareTarget {
    label: string
    icon: Component<{ class?: string }>
    href: string
  }

  const targets: ShareTarget[] = [
    {
      label: 'X',
      icon: XIcon,
      href: `https://twitter.com/intent/tweet?text=${encodeURIComponent(SHARE_TEXT)}&url=${encodeURIComponent(SHARE_URL)}`,
    },
    {
      label: 'LinkedIn',
      icon: LinkedinIcon,
      href: `https://www.linkedin.com/sharing/share-offsite/?url=${encodeURIComponent(SHARE_URL)}`,
    },
    {
      label: 'Bluesky',
      icon: BlueskyIcon,
      href: `https://bsky.app/intent/compose?text=${encodeURIComponent(`${SHARE_TEXT} ${SHARE_URL}`)}`,
    },
    {
      label: 'Reddit',
      icon: RedditIcon,
      href: `https://www.reddit.com/submit?url=${encodeURIComponent(SHARE_URL)}&title=${encodeURIComponent(SHARE_TEXT)}`,
    },
    {
      label: 'Hacker News',
      icon: HackerNewsIcon,
      href: `https://news.ycombinator.com/submitlink?u=${encodeURIComponent(SHARE_URL)}&t=${encodeURIComponent(SHARE_TEXT)}`,
    },
    {
      label: 'Email',
      icon: Mail,
      href: `mailto:?subject=${encodeURIComponent(SHARE_TEXT)}&body=${encodeURIComponent(SHARE_URL)}`,
    },
  ]

  let open = $state(false)
  let copied = $state(false)

  function share(href: string): void {
    void openURL(href)
    open = false
  }

  async function copyLink(): Promise<void> {
    await navigator.clipboard.writeText(SHARE_URL)
    copied = true
    setTimeout(() => {
      copied = false
      open = false
    }, 900)
  }

  function onWindowPointerDown(event: PointerEvent): void {
    if (!open) return
    const target = event.target as HTMLElement | null
    if (!target?.closest('[data-share-menu]')) open = false
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') open = false
  }
</script>

<svelte:window onpointerdown={onWindowPointerDown} onkeydown={onKeydown} />

<div class="relative" data-share-menu>
  <button
    type="button"
    onclick={() => (open = !open)}
    aria-expanded={open}
    aria-haspopup="menu"
    class="state-layer flex cursor-pointer items-center gap-1 rounded-xs px-1 opacity-70
           transition-opacity duration-100 hover:opacity-100"
  >
    Share on
    <Share2 class="size-3" strokeWidth={2} />
  </button>

  {#if open}
    <div
      class="absolute bottom-full left-0 z-50 mb-1.5 w-48 overflow-hidden rounded-sm
             border border-outline-variant/60 bg-surface-container-high py-1.5 shadow-level-2"
      role="menu"
    >
      <p class="px-3 py-1 text-label-small font-semibold uppercase tracking-wider text-on-surface-variant/60">
        Share PodSteer
      </p>

      {#each targets as target (target.label)}
        {@const Icon = target.icon}
        <button
          type="button"
          onclick={() => share(target.href)}
          role="menuitem"
          class="state-layer flex w-full cursor-pointer items-center gap-2.5 px-3 py-1.5 text-left
                 text-body-medium text-on-surface transition-colors duration-75
                 hover:bg-surface-container-highest"
        >
          <Icon class="size-3.5 shrink-0 text-on-surface-variant/70" />
          {target.label}
        </button>
      {/each}

      <div class="my-1 border-t border-outline-variant/30"></div>

      <button
        type="button"
        onclick={copyLink}
        role="menuitem"
        class="state-layer flex w-full cursor-pointer items-center gap-2.5 px-3 py-1.5 text-left
               text-body-medium transition-colors duration-75 hover:bg-surface-container-highest
               {copied ? 'text-success' : 'text-on-surface'}"
      >
        {#if copied}
          <Check class="size-3.5 shrink-0" strokeWidth={2.5} />
          Copied!
        {:else}
          <Copy class="size-3.5 shrink-0 text-on-surface-variant/70" />
          Copy link
        {/if}
      </button>
    </div>
  {/if}
</div>
