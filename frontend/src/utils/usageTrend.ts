import dayjs from 'dayjs'

export type TrendRange = 'today' | 'week' | 'month'
export interface TrendPoint { label: string; cpu: number | null; mem: number | null }

// Only observed, healthy utilization samples belong on a utilization chart.
// Temperature, missing readings and absent time buckets must never become usage values.
export function usageTrend(records: Record<string, unknown>[], range: TrendRange, now = dayjs()): TrendPoint[] {
  const slots = range === 'today'
    ? Array.from({ length: now.hour() + 1 }, (_, h) => now.startOf('day').hour(h))
    : Array.from({ length: range === 'week' ? 7 : 30 }, (_, i) => now.startOf('day').subtract((range === 'week' ? 7 : 30) - 1 - i, 'day'))
  const keyOf = (at: ReturnType<typeof dayjs>) => at.format(range === 'today' ? 'YYYY-MM-DDTHH' : 'YYYY-MM-DD')
  const buckets = new Map(slots.map(at => [keyOf(at), { cpu: [] as number[], mem: [] as number[] }]))
  for (const record of records) {
    if (record.quality !== 'good' || record.value == null || record.value === '') continue
    const metric = `${record.metric ?? ''} ${record.sensor_key ?? ''}`.toLowerCase()
    if (/temperature|thermal|celsius|fahrenheit/.test(metric) || !/usage|utilization|utilisation|percent/.test(metric)) continue
    const kind = /cpu|processor/.test(metric) ? 'cpu' : /memory|mem|ram/.test(metric) ? 'mem' : null
    const unit = String(record.unit ?? '').trim().toLowerCase()
    const value = Number(record.value)
    if (!kind || !['%', 'percent', 'percentage'].includes(unit) || !Number.isFinite(value) || value < 0 || value > 100) continue
    const at = dayjs(String(record.sampled_at ?? ''))
    if (!at.isValid() || at.isAfter(now)) continue
    buckets.get(keyOf(at))?.[kind].push(value)
  }
  const average = (values: number[]) => values.length ? Math.round(values.reduce((sum, value) => sum + value, 0) / values.length * 10) / 10 : null
  return slots.map(at => {
    const bucket = buckets.get(keyOf(at))!
    return { label: at.format(range === 'today' ? 'HH:00' : 'M/D'), cpu: average(bucket.cpu), mem: average(bucket.mem) }
  })
}
