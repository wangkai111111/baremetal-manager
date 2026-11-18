import { test } from 'node:test'
import assert from 'node:assert/strict'
import { flattenDepartments, assetIsOnline } from '../src/utils/departments.ts'

test('department options include descendants and unambiguous full paths', () => {
 const input = [{ id: 1, name: '技术中心', children: [{ id: 2, name: '平台组', children: [{ id: 3, name: '运维' }] }] }, { id: 4, name: '运营', children: [{ id: 5, name: '运维' }] }]
 assert.deepEqual(flattenDepartments(input), [
  { id: 1, name: '技术中心' }, { id: 2, name: '技术中心 / 平台组' },
  { id: 3, name: '技术中心 / 平台组 / 运维' }, { id: 4, name: '运营' }, { id: 5, name: '运营 / 运维' }
 ])
 assert.equal(input[0].children[0].name, '平台组')
 assert.deepEqual(flattenDepartments([]), [])
})

test('invalid IDs, repeated nodes and cycles do not produce unusable options', () => {
 const node = { id: 1, name: '部门', children: [] as unknown[] }
 node.children.push(node)
 assert.deepEqual(flattenDepartments([node, { id: 0 }, { id: 'bad' }]), [{ id: 1, name: '部门' }])
})

test('online metrics reflect power state independently of legacy status', () => {
 assert.equal(assetIsOnline({ power_state: 'on', status: 'unknown' }), true)
 assert.equal(assetIsOnline({ power_state: 'off', status: 'online' }), false)
 assert.equal(assetIsOnline({}), false)
})
