import test from 'node:test'
import assert from 'node:assert/strict'
import { hasPermission } from '../src/utils/permissions.ts'

test('only explicit grants or platform administration authorize an action', () => {
  assert.equal(hasPermission(false, ['assets:read'], 'assets:read'), true)
  assert.equal(hasPermission(false, ['assets:write'], 'assets:read'), false)
  assert.equal(hasPermission(false, ['*'], 'iam:manage'), false)
  assert.equal(hasPermission(false, [], 'settings:read'), false)
  assert.equal(hasPermission(true, [], 'iam:manage'), true)
  assert.equal(hasPermission(false, []), true)
})
test('switching project does not retain grants from the previous project', () => {
  const oldGrants = ['iam:manage']
  const newGrants = ['assets:read']
  assert.equal(hasPermission(false, oldGrants, 'iam:manage'), true)
  assert.equal(hasPermission(false, newGrants, 'iam:manage'), false)
})
