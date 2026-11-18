type Department = { id?: unknown; name?: unknown; children?: unknown }

// The department endpoint returns a tree, while selects and asset labels need paths.
export function flattenDepartments(nodes: Department[]): { id: number; name: string }[] {
  const result: { id: number; name: string }[] = []
  const seen = new Set<number>()
  function visit(items: Department[], parents: string[]) {
    for (const node of items) {
      const id = Number(node.id)
      if (!Number.isSafeInteger(id) || id <= 0 || seen.has(id)) continue
      seen.add(id)
      const path = [...parents, String(node.name || `部门 #${id}`)]
      result.push({ id, name: path.join(' / ') })
      if (Array.isArray(node.children)) visit(node.children, path)
    }
  }
  visit(nodes, [])
  return result
}

export function assetIsOnline(asset: Record<string, unknown>): boolean {
  return asset.power_state === 'on'
}
