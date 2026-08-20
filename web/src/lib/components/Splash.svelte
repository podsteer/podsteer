<!--
  Splash screen, shown while the workspace reads the kubeconfig and re-adopts
  anything the backend still holds from a hot reload.

  Deliberately always dark, whatever theme the operator picked: the Go side
  paints the window in the dark surface colour before the webview renders, and
  a splash that matched the window instead of the theme means there is never
  a colour flip at launch. Theme-aware chrome appears the moment this fades.

  The background is pure CSS plus an inline SVG — no image to ship — and stays
  inside the CSP (style-src allows inline styles).
-->
<script lang="ts">
  import { fade } from 'svelte/transition'
  import { appInfo } from '$stores/system.svelte'
</script>

<div
  class="splash fixed inset-0 z-[100] flex flex-col items-center justify-center"
  role="status"
  aria-label="Starting K8Sense"
  transition:fade={{ duration: 280 }}
>
  <div class="mark" aria-hidden="true">
    <svg viewBox="0 0 96 96" fill="none" class="size-24">
      <defs>
        <linearGradient id="splash-edge" x1="0" y1="0" x2="96" y2="96" gradientUnits="userSpaceOnUse">
          <stop stop-color="#8ab4f8" />
          <stop offset="1" stop-color="#8bd5a0" />
        </linearGradient>
      </defs>
      <!--
        The K8Sense mark (see brand/k8sense-mark.svg), in the splash's own
        gradient rather than the brand blue, because it sits on the splash's
        dark ground.

        It replaced a seven-sided helm with spokes — which is to say, a
        Kubernetes logo derivative. The Kubernetes mark is a CNCF trademark and
        may not be used as another product's logo, however it is redrawn. Six
        sides owe nobody anything.
      -->
      <g fill="none" stroke="url(#splash-edge)" stroke-linecap="round" stroke-linejoin="round">
        <path d="M48 16 75.7 32 75.7 64 48 80 20.3 64 20.3 32Z" stroke-width="5.2"/>
        <path d="M30.4 49.6h8l5.6-13.6 7.2 24 5.6-10.4h8.8" stroke-width="6.4"/>
      </g>
    </svg>
  </div>

  <h1 class="wordmark mt-8 text-[36px] leading-[44px] font-medium tracking-tight">K8Sense</h1>
  <p class="mt-2 text-[13px] leading-4 text-[#cac4d0]">A fast, native Kubernetes client</p>

  <div class="loading mt-10 h-0.5 w-40 overflow-hidden rounded-full" aria-hidden="true">
    <div class="loading-bar"></div>
  </div>

  {#if appInfo.version !== '—'}
    <p class="absolute bottom-6 text-[11px] text-[#938f99] tabular-nums">
      v{appInfo.version} · {appInfo.platform}
    </p>
  {/if}
</div>

<style>
  .splash {
    background-color: #141218;
    background-image:
      radial-gradient(640px 400px at 50% -10%, rgb(138 180 248 / 0.15), transparent 70%),
      radial-gradient(560px 380px at 88% 108%, rgb(139 213 160 / 0.1), transparent 70%),
      radial-gradient(420px 300px at 6% 96%, rgb(239 184 200 / 0.07), transparent 70%);
  }

  /* A faint hexagon honeycomb, the cluster-graph motif, tiled behind the glows. */
  .splash::before {
    content: '';
    position: absolute;
    inset: 0;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='56' height='97' viewBox='0 0 56 97'%3E%3Cg fill='none' stroke='%23ffffff' stroke-opacity='0.05'%3E%3Cpath d='M28 0l24 14v28L28 56 4 42V14z'/%3E%3Cpath d='M28 56l24 14v28L28 112 4 98V70z' transform='translate(0 -15)'/%3E%3C/g%3E%3C/svg%3E");
    mask-image: radial-gradient(720px 480px at 50% 42%, black, transparent 78%);
  }

  .mark {
    position: relative;
    animation: float 5s ease-in-out infinite;
  }

  .mark::after {
    content: '';
    position: absolute;
    inset: -28%;
    border-radius: 9999px;
    background: radial-gradient(closest-side, rgb(138 180 248 / 0.16), transparent);
    filter: blur(2px);
    animation: pulse 2.4s ease-in-out infinite;
  }

  .wordmark {
    background: linear-gradient(100deg, #d3e3fd 10%, #8ab4f8 45%, #a6f0bb 95%);
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
  }

  .loading {
    background: rgb(255 255 255 / 0.09);
  }

  .loading-bar {
    width: 40%;
    height: 100%;
    border-radius: 9999px;
    background: linear-gradient(90deg, #8ab4f8, #a6f0bb);
    animation: slide 1.1s cubic-bezier(0.45, 0, 0.55, 1) infinite;
  }

  @keyframes slide {
    from {
      transform: translateX(-110%);
    }
    to {
      transform: translateX(400%);
    }
  }

  @keyframes float {
    0%,
    100% {
      transform: translateY(0);
    }
    50% {
      transform: translateY(-6px);
    }
  }

  @keyframes pulse {
    0%,
    100% {
      opacity: 0.5;
    }
    50% {
      opacity: 1;
    }
  }

  /* The base layer kills animations under prefers-reduced-motion; the splash
     still renders, just static. */
</style>
