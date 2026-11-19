import assert from 'node:assert/strict'
import test from 'node:test'
import {channelDraft, channelPayload} from '../src/utils/notificationOptions.ts'

test('webhook secrets are required for creation and preserved on edit', () => {
  const form = {...channelDraft(), name:'值班群'}
  assert.throws(() => channelPayload(form, false), /完整 Webhook/)
  assert.deepEqual(channelPayload(form, true), {name:'值班群', kind:'wechat', enabled:true, config:{}})
  form.secret = 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test-only'
  assert.equal(channelPayload(form, false).secret, form.secret)
  for (const secret of ['http://example.com/hook', 'https://example.com/hook', 'https://u:p@qyapi.weixin.qq.com/hook', 'https://qyapi.weixin.qq.com:8443/hook']) {
    assert.throws(() => channelPayload({...form, secret}, false))
  }
})

test('switching from email to webhook drops all email configuration', () => {
  const form = channelDraft({name:'邮件', kind:'email', config:{host:'smtp.example.com', port:465, username:'mailer', from:'a@example.com', to:'b@example.com', tls:true}})
  form.kind = 'webhook'; form.secret = 'https://example.com/hook'
  assert.deepEqual(channelPayload(form, false).config, {})
})

test('SMTP credentials can be preserved and explicitly cleared by disabling auth', () => {
  const form = channelDraft({name:'值班邮件', kind:'email', config:{host:'smtp.example.com', port:587, username:'mailer', from:'a@example.com', to:'b@example.com', tls:true}})
  assert.throws(() => channelPayload(form, false), /账号及密码/)
  assert.equal(Object.hasOwn(channelPayload(form, true), 'secret'), false)
  form.auth = false
  const payload = channelPayload(form, true)
  assert.equal(payload.secret, '')
  assert.equal((payload.config as {username:string}).username, '')
  form.auth = true; form.secret = 'test-only'; form.tls = false
  assert.throws(() => channelPayload(form, true), /STARTTLS/)
  form.port = 465
  assert.doesNotThrow(() => channelPayload(form, false))
  form.port = 1.5
  assert.throws(() => channelPayload(form, false), /整数/)
})

test('unsupported channel kinds and multiple recipients are rejected', () => {
  const form = {...channelDraft(), name:'test', kind:'unknown'}
  assert.throws(() => channelPayload(form, true), /类型/)
  const email = channelDraft({name:'test', kind:'email', config:{host:'smtp.example.com', port:25, from:'a@example.com', to:'b@example.com,c@example.com'}})
  assert.throws(() => channelPayload(email, false), /单个/)
})
