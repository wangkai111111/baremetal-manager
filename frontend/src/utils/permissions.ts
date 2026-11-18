// Match the API's exact permission checks. A role name or write permission
// cannot imply unrelated access, and only platform admins bypass the grants.
export function hasPermission(admin: boolean, grants: readonly string[], permission?: string): boolean {
  return !permission || admin || grants.includes(permission)
}
