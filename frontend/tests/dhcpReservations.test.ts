import assert from 'node:assert/strict'
import test from 'node:test'
import {newDHCPReservation, validateDHCPReservations} from '../src/utils/dhcpReservations.ts'
const context = {subnet: '10.30.0.0/24', gateway: '10.30.0.1', server: '172.16.20.23', mode: 'relay', taggedOnly: false, relayProxy: false}
const rule = () => ({...newDHCPReservation(), name: '机柜 A / 端口 1', circuit_id: 'port1', ip: '10.30.0.110'})
test('relay rules accept exact IDs and fixed addresses inside or outside the dynamic pool', () => {
  assert.equal(validateDHCPReservations([rule()], context), '')
  assert.equal(validateDHCPReservations([{...rule(), ip: '10.30.0.20'}], context), '')
  assert.equal(validateDHCPReservations([{...rule(), circuit_id: '', mac: '02:00:00:00:00:01'}], {...context, mode: 'direct'}), '')
})
test('missing matches, unsafe IDs and invalid infrastructure IPs cannot be saved', () => {
  for (const change of [{circuit_id: ''}, {circuit_id: '00:zz', circuit_format: 'hex' as const}, {circuit_id: 'port\nport=53'}, {ip: '10.30.0.1'}, {ip: '10.30.0.0'}, {ip: '10.30.0.255'}, {ip: '192.0.2.1'}, {mac: '01:00:00:00:00:01'}, {mac: '00:00:00:00:00:00'}]) assert.ok(validateDHCPReservations([{...rule(), ...change}], context))
  assert.match(validateDHCPReservations([rule()], {...context, mode: 'direct'}), /Relay/)
})
test('overlapping matches compare bytes across text and hex and can be disambiguated by Remote-ID', () => {
  const one = rule(), two = {...rule(), name: '另一个端口', ip: '10.30.0.111', circuit_id: '70:6f:72:74:31', circuit_format: 'hex' as const}
  assert.match(validateDHCPReservations([one, two], context), /同时命中/)
  assert.equal(validateDHCPReservations([{...one, remote_id: 'switch-a'}, {...two, remote_id: 'switch-b'}], context), '')
  assert.match(validateDHCPReservations([one, {...two, circuit_id: 'other', circuit_format: 'text', ip: one.ip}], context), /固定 IP 已/)
})
test('strict mode requires an enabled rule; disabled rules do not conflict', () => {
  assert.match(validateDHCPReservations([{...rule(), enabled: false}], {...context, taggedOnly: true}), /已启用/)
  assert.equal(validateDHCPReservations([rule(), {...rule(), enabled: false}], context), '')
  assert.match(validateDHCPReservations([], {...context, mode: 'direct', relayProxy: true}), /代理/)
})
