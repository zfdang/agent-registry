/**
 * The agent URL is the canonical key (docs/03, docs/04). Detail links use
 * the scheme-stripped form of the registered URL, e.g.
 * `https://agents.acme.example/agent` -> `/agents/agents.acme.example/agent`.
 */
export function stripScheme(url: string): string {
  return url.replace(/^https?:\/\//, '')
}

export function agentDetailHref(url: string): string {
  return `/agents/${stripScheme(url)}`
}
