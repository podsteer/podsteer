/**
 * Pure helpers for the local terminal and the coding-agent bridge.
 *
 * Kept out of the components for the reason `debugShell.ts` is: what a
 * confirmed dialog actually asks for — which agent, whether the read-only
 * default is on, what the pane is titled — is a set of small decisions that
 * are worth arguing with in a test rather than observing in the UI.
 *
 * NOTHING HERE TALKS TO A CLUSTER, and nothing here launches anything. The
 * launch is one bound call; these decide what to pass to it.
 */

/** A coding agent the Go side found on the operator's PATH. */
export interface CodingAgent {
  id: string
  label: string
  path: string
}

/** What a confirmed local-terminal dialog asks for. */
export interface LocalShellRequest {
  /**
   * The agent to run, or null for the operator's own login shell.
   *
   * Null rather than an empty string so "no agent" cannot be confused with
   * "an agent whose id did not survive a form" — the backend refuses an empty
   * id outright, and this is what keeps that refusal unreachable from here.
   */
  agent: string | null
  /**
   * Whether to ask the agent to keep to read-only kubectl.
   *
   * Meaningless for a plain shell and normalised to false there, because the
   * marker's whole meaning is that somebody asked an agent something.
   */
  readOnly: boolean
}

/**
 * Normalises a local-terminal dialog's inputs.
 *
 * An agent id that names nothing detected falls back to a plain shell rather
 * than being sent for the backend to refuse: the list came from detection a
 * moment ago, so an id that is no longer in it means the agent went away, and
 * opening the shell the operator would otherwise have had beats an error
 * dialog.
 */
export function localShellRequest(
  agentId: string,
  readOnly: boolean,
  available: CodingAgent[],
): LocalShellRequest {
  const id = agentId.trim()
  const known = available.some((agent) => agent.id === id)
  if (id === '' || !known) return { agent: null, readOnly: false }
  return { agent: id, readOnly }
}

/**
 * Names the pane, so the tab strip says what is running in it.
 *
 * The agent's own label when there is one, because "Claude Code" is what the
 * operator asked for and "Local shell" would hide it; the plain name
 * otherwise.
 */
export function localShellTitle(request: LocalShellRequest, available: CodingAgent[]): string {
  if (request.agent === null) return 'Local shell'
  return available.find((agent) => agent.id === request.agent)?.label ?? request.agent
}

/**
 * The sentence a local terminal shows above its buffer.
 *
 * IT SAYS THE GUARD DOES NOT APPLY, and that is the point of it. Every other
 * terminal in this application refuses to open on a cluster marked read-only.
 * This one does not, because that guard is about PodSteer's own writes — a
 * shell the operator opened on their own machine, with their own credentials,
 * is not something this application can or should police. Saying so is more
 * honest than a pane that quietly behaves differently from the one beside it.
 */
export function localShellNotice(context: string): string {
  const cluster = context === '' ? 'no cluster tab' : `context "${context}"`
  return (
    `Your own shell, on this machine, with your own credentials. ` +
    `KUBECONFIG is set to the files PodSteer reads and ${cluster} is open — ` +
    `current-context is untouched, so pass --context. ` +
    `PodSteer's read-only setting does not apply here: it governs PodSteer's own writes, ` +
    `not a shell you opened yourself.`
  )
}
