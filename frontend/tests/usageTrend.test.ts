import assert from 'node:assert/strict'
import test from 'node:test'
import dayjs from 'dayjs'
import { usageTrend } from '../src/utils/usageTrend.ts'

const now = dayjs('2026-11-16T03:30:00')
const sample = (value: unknown, at = '2026-11-16T01:10:00') => ({ metric: 'cpu_utilization', unit: '%', quality: 'good', value, sampled_at: at })

test('hourly samples are assigned to their real hour and averaged', () => {
  const result = usageTrend([sample(10), sample(30), sample(80, '2026-11-16T03:10:00')], 'today', now)
  assert.deepEqual(result.map(point => point.cpu), [null, 20, null, 80])
  assert.deepEqual(result.map(point => point.label), ['00:00', '01:00', '02:00', '03:00'])
})
test('missing, unhealthy, thermal, invalid and future samples cannot fabricate utilization', () => {
  const invalid = [sample(null), sample(''), sample(120), { ...sample(30), quality: 'failed' }, { ...sample(40), metric: 'cpu_temperature', unit: 'C' }, sample(40, '2026-11-16T04:00:00')]
  assert.ok(usageTrend(invalid, 'today', now).every(point => point.cpu === null && point.mem === null))
  assert.ok(usageTrend([], 'month', now).every(point => point.cpu === null && point.mem === null))
})
test('zero is a valid observation; gaps are not forward filled', () => {
  const result = usageTrend([{ ...sample(0), metric: 'memory_usage' }], 'today', now)
  assert.equal(result[1].mem, 0)
  assert.equal(result[2].mem, null)
})
