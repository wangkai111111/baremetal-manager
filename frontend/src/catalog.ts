export interface Field {
  key: string
  label: string
  type?: 'select' | 'textarea' | 'password' | 'number' | 'json' | 'switch'
  options?: string[]
  required?: boolean
  placeholder?: string
  help?: string
  default?: string | number | boolean
  min?: number
  max?: number
}

export interface Section {
  path: string
  title: string
  subtitle: string
  api: string
  permission: string
  fields: Field[]
  columns?: string[]
  readonly?: boolean
}

const field = (key: string, label: string, required = false): Field => ({
  key,
  label,
  required
})

export const sections: Section[] = [
  { path: '/tenants', title: '组织管理', subtitle: '管理内部组织与访问边界', api: '/tenants', permission: 'projects:read', fields: [field('name', '组织名称', true), field('code', '编码', true), field('description', '描述')], columns: ['name', 'code', 'description', 'created_at'] },
  {
    path: '/projects',
    title: '项目管理',
    subtitle: '组织业务资源与访问边界',
    api: '/projects',
    permission: 'projects:read',
    fields: [
      field('name', '项目名称', true),
      field('description', '描述')
    ],
    columns: ['name', 'description', 'created_at']
  },
  {
    path: '/datacenters',
    title: '机房管理',
    subtitle: '管理物理站点与联系信息',
    api: '/datacenters',
    permission: 'datacenters:read',
    fields: [
      field('name', '机房名称', true),
      field('location', '位置'),
      field('contact', '联系人')
    ]
  },
  {
    path: '/assets',
    title: '裸机资产',
    subtitle: '发现、管理并维护物理服务器',
    api: '/assets',
    permission: 'assets:read',
    fields: [
      field('name', '资产名称', true),
      field('bmc_address', 'BMC 地址', true),
      field('bmc_user', 'BMC 用户名'),
      { ...field('bmc_secret', 'BMC 密码'), type: 'password' },
      { ...field('bmc_port', 'BMC 端口'), type: 'number' },
      { ...field('bmc_protocol', 'BMC 协议'), type: 'select', options: ['redfish', 'ipmi'] },
      field('management_ip', '管理 IP'),
      { ...field('datacenter_id', '机房'), type: 'select' }
    ],
    columns: ['name', 'bmc_address', 'management_ip', 'datacenter_id', 'status', 'created_at']
  },
  {
    path: '/images',
    title: '镜像仓库',
    subtitle: '公有镜像目录与项目内可用镜像',
    api: '/images',
    permission: 'images:read',
    fields: [field('name', '镜像名称', true), field('os_type', '操作系统', true), field('version', '版本', true), field('architecture', '架构', true), field('object_key', '存储对象键', true), { ...field('size_bytes', '大小（字节）', true), type: 'number' }, field('sha256', 'SHA256', true)],
    columns: ['name', 'os_type', 'version', 'status', 'created_at']
  },
  {
    path: '/subnets',
    title: 'IPAM · 子网',
    subtitle: '先规划子网，再创建地址池；允许 DHCP 仅代表可用于装机，不会启动网络服务。',
    api: '/networks',
    permission: 'network:read',
    fields: [
      { ...field('name', '子网名称', true), placeholder: '例如：机房装机网络' },
      { ...field('subnet', '子网 CIDR', true), placeholder: '192.168.10.0/24', help: '填写网络地址；IPv6 可用于 IPAM，当前 PXE 使用 IPv4。' },
      { ...field('gateway', '默认网关'), placeholder: '192.168.10.1（可留空）', help: '必须是网段内的可用地址，不能落入地址池。' },
      { ...field('vlan_id', 'VLAN ID'), type: 'number', min: 1, max: 4094, help: '1–4094；留空表示不打标，登记不会创建网卡或交换机 VLAN。' },
      { ...field('dns_servers', 'DNS 服务器'), type: 'textarea', placeholder: '192.168.10.1, 223.5.5.5', help: '选填，逗号或换行分隔，最多 8 个 IP；仅记录网络规划。' },
      { ...field('dhcp_enabled', '允许 DHCP 装机'), type: 'switch', default: false, help: '允许全局 PXE 服务绑定此子网；实际启停在 PXE 配置中操作。' }
    ],
    columns: ['name', 'subnet', 'gateway', 'vlan_id', 'dns_servers', 'dhcp_enabled', 'created_at']
  },
  {
    path: '/alerts',
    title: '监控告警',
    subtitle: '查看事件、异常与处理进度',
    api: '/alerts',
    permission: 'alerts:read',
    fields: [],
    columns: ['severity', 'message', 'asset_id', 'status', 'created_at'],
    readonly: true
  },
  // === 新增：告警规则 ===
  {
    path: '/alert-rules',
    title: '告警规则',
    subtitle: '自定义监控阈值与告警策略',
    api: '/alert-rules',
    permission: 'alerts:manage',
    fields: [
      field('name', '规则名称', true),
      field('metric', '监控指标', true),
      field('operator', '运算符', true),
      { ...field('threshold', '阈值（collection_failed 留空）'), type: 'number' },
      { ...field('severity', '告警等级', true), type: 'select', options: ['info', 'warning', 'critical'] },
      { ...field('duration_seconds', '持续时间（内置监控请填 0）'), type: 'number' }
    ],
    columns: ['name', 'metric', 'threshold', 'severity', 'enabled', 'created_at']
  },
  // === 新增：推送渠道 ===
  {
    path: '/alert-channels',
    title: '推送渠道',
    subtitle: '配置告警通知渠道',
    api: '/alert-channels',
    permission: 'alerts:manage',
    fields: [
      field('name', '渠道名称', true),
      { ...field('kind', '渠道类型', true), type: 'select', options: ['email', 'webhook', 'lark'] },
      { ...field('config', '非敏感配置(JSON)', true), type: 'json' },
      { ...field('secret', 'SMTP 密码或 HTTPS Webhook 完整地址'), type: 'password' }
    ],
    columns: ['name', 'kind', 'enabled', 'last_test_status', 'last_test_at', 'last_test_message', 'created_at']
  },
  // === 新增：IP 地址池 ===
  {
    path: '/ip-pools',
    title: 'IP 地址池',
    subtitle: '选择子网后设置连续的可分配范围，避开网关、网络地址与广播地址；同一子网内地址池不能重叠。',
    api: '/ip-pools',
    permission: 'network:write',
    fields: [
      field('name', '地址池名称', true),
      { ...field('network_id', '所属子网', true), type: 'select', help: '仅可选择当前项目内的子网。' },
      { ...field('start_address', '起始 IP', true), placeholder: '192.168.10.100' },
      { ...field('end_address', '结束 IP', true), placeholder: '192.168.10.200', help: '包含起始和结束地址；PXE 服务运行期间不能修改已绑定的地址池范围。' }
    ],
    columns: ['name', 'network_id', 'start_address', 'end_address', 'created_at']
  },
  // === 新增：标签管理 ===
  {
    path: '/tags',
    title: '标签管理',
    subtitle: '自定义标签分类与筛选',
    api: '/tags',
    permission: 'assets:write',
    fields: [
      field('name', '标签名称', true),
      { ...field('color', '标签颜色'), type: 'select', options: ['#267b82', '#e5a65c', '#d36b65', '#526b88', '#48b7a3'] }
    ],
    columns: ['name', 'color', 'created_at']
  },
  // === 新增：API Key 管理 ===
  {
    path: '/api-keys',
    title: 'API 密钥',
    subtitle: '管理开放 API 访问密钥',
    api: '/api-keys',
    permission: 'iam:write',
    fields: [
      field('name', '密钥名称', true),
      field('permissions', '权限列表'),
      field('expires_at', '过期时间')
    ],
    columns: ['name', 'permissions', 'last_used_at', 'expires_at', 'created_at'],
    readonly: true
  },
  // === 新增：项目成员 ===
  {
    path: '/project-members',
    title: '项目成员',
    subtitle: '管理项目成员与权限分配',
    api: '/members',
    permission: 'iam:read',
    fields: [
      field('user_id', '用户 ID', true),
      { ...field('role', '角色'), type: 'select', options: ['admin', 'manager', 'operator', 'viewer'] }
    ],
    columns: ['user_id', 'role', 'created_at']
  },
  {
    path: '/firmware',
    title: '固件管理',
    subtitle: '固件升级暂不支持；可查看任务中心已有记录',
    api: '/tasks',
    permission: 'tasks:read',
    fields: [],
    columns: ['kind', 'bmc_ip', 'operator_name', 'client_ip', 'status', 'installation_stage', 'error', 'created_at'],
    readonly: true
  },
  {
    path: '/users',
    title: '用户管理',
    subtitle: '用户账户与访问控制',
    api: '/users',
    permission: 'iam:read',
    fields: [
      field('username', '用户名', true),
      field('email', '邮箱'),
      field('password', '初始密码')
    ],
    columns: ['username', 'email', 'status', 'created_at']
  },
  {
    path: '/roles',
    title: '角色权限',
    subtitle: '维护角色及授权范围',
    api: '/roles',
    permission: 'iam:read',
    fields: [
      field('name', '角色名称', true),
      field('description', '描述')
    ],
    columns: ['name', 'description', 'created_at']
  },
  {
    path: '/tasks',
    title: '任务中心',
    subtitle: '异步作业执行状态与结果',
    api: '/tasks',
    permission: 'tasks:read',
    fields: [],
    columns: ['kind', 'bmc_ip', 'operator_name', 'client_ip', 'status', 'installation_stage', 'error', 'created_at'],
    readonly: true
  },
  {
    path: '/audit-logs',
    title: '审计日志',
    subtitle: '关键操作与访问轨迹',
    api: '/audit',
    permission: 'audit:read',
    fields: [],
    columns: ['user_id', 'action', 'target_type', 'target_id', 'outcome', 'created_at'],
    readonly: true
  },
  {
    path: '/settings',
    title: '系统设置',
    subtitle: '平台参数与集成配置',
    api: '/settings',
    permission: 'settings:read',
    fields: [
      field('key', '配置项', true),
      field('value', '配置值', true),
      field('description', '描述')
    ],
    columns: ['key', 'value', 'description']
  },
  // === 自用模式专属：部门管理 ===
  {
    path: '/departments',
    title: '部门管理',
    subtitle: '组织部门结构与人员归属',
    api: '/departments',
    permission: 'departments:read',
    fields: [
      field('name', '部门名称', true),
      { ...field('parent_id', '上级部门 ID'), type: 'number' },
      field('description', '描述')
    ],
    columns: ['name', 'parent_id', 'description', 'created_at']
  },
  // === 自用模式专属：资产折旧 ===
  {
    path: '/asset-depreciation',
    title: '资产折旧',
    subtitle: '追踪资产折旧与残值',
    api: '/assets/depreciation',
    permission: 'assets:read',
    fields: [
      { ...field('asset_id', '资产 ID', true), type: 'number' },
      field('asset_name', '资产名称'),
      field('depreciation_method', '折旧方式'),
      { ...field('useful_life_months', '使用年限（月）'), type: 'number' },
      { ...field('purchase_cost', '资产原值'), type: 'number' },
      { ...field('salvage_value', '残值'), type: 'number' }
    ],
    columns: ['asset_id', 'asset_name', 'depreciation_method', 'purchase_cost', 'useful_life_months', 'salvage_value', 'created_at'],
    readonly: true
  },
  // === 自用模式专属：维保告警 ===
  {
    path: '/warranty-alerts',
    title: '维保告警',
    subtitle: '资产维保到期预警',
    api: '/assets/warranty-alerts',
    permission: 'assets:read',
    fields: [],
    columns: ['asset_id', 'name', 'warranty_expiry', 'days_remaining', 'status'],
    readonly: true
  },
  // === 工单系统 ===
  {
    path: '/work-orders',
    title: '工单管理',
    subtitle: '服务请求与故障处理',
    api: '/work-orders',
    permission: 'work_orders:read',
    fields: [
      { ...field('type', '工单类型'), type: 'select', options: ['fault', 'technical', 'consultation', 'other'] },
      field('title', '标题', true),
      { ...field('description', '描述'), type: 'textarea' },
      { ...field('status', '状态'), type: 'select', options: ['open', 'in_progress', 'resolved', 'closed'] },
      { ...field('priority', '优先级'), type: 'select', options: ['low', 'medium', 'high', 'urgent'] }
    ],
    columns: ['type', 'title', 'status', 'priority', 'created_at']
  },
]

export const labels: Record<string, string> = {
  id: 'ID',
  name: '名称',
  code: '编码',
  description: '描述',
  status: '状态',
  enabled: '已启用',
  created_at: '创建时间',
  updated_at: '更新时间',
  last_used_at: '最后使用时间',
  hostname: '主机名',
  bmc_address: 'BMC 地址',
  bmc_user: 'BMC 用户名',
  bmc_secret: 'BMC 密码',
  ip_address: '业务 IP',
  management_ip: '管理 IP',
  bmc_port: 'BMC 端口',
  bmc_protocol: 'BMC 协议',
  power_state: '电源状态',
  tenant_id: '组织 ID',
  mac_address: 'MAC 地址',
  size: '大小 (字节)',
  kind: '任务类型',
  resolved: '已解决',
  target: '目标',
  target_type: '资源类型',
  target_id: '资源 ID',
  outcome: '操作结果',
  subnet: 'CIDR',
  vlan_id: 'VLAN ID',
  dhcp_enabled: 'DHCP',
  start_address: '起始 IP',
  end_address: '结束 IP',
  user_id: '用户 ID',
  network_id: '子网 ID',
  error: '错误',
  serial_number: '序列号',
  bmc_ip: 'BMC IP',
  operator_name: '操作人',
  client_ip: '操作 IP',
  installation_stage: '安装阶段',
  project_id: '项目 ID',
  datacenter_id: '机房',
  location: '位置',
  contact: '联系人',
  asset_id: '资产 ID',
  image_id: '镜像 ID',
  os_type: '操作系统',
  version: '版本',
  address: '地址',
  cidr: '网段',
  gateway: '网关',
  severity: '级别',
  message: '内容',
  metric: '监控指标',
  operator: '运算符',
  threshold: '阈值',
  time_window: '时间窗口',
  type: '类型',
  config: '配置',
  progress: '进度',
  actor: '操作人',
  action: '操作',
  role: '角色',
  resource: '资源',
  username: '用户名',
  email: '邮箱',
  password: '密码',
  component: '组件',
  key: '配置项',
  value: '配置值',
  color: '颜色',
  permissions: '权限列表',
  expires_at: '过期时间',
  kernel: '内核',
  initrd: 'Initrd',
  append: '启动参数',
  template_content: '模板内容',
  partition_scheme: '分区方案',
  post_script: '装机后脚本',
  start_ip: '起始 IP',
  end_ip: '结束 IP',
  // 新增字段标签
  parent_id: '上级部门',
  useful_life_months: '使用年限（月）',
  residual_value: '残值',
  asset_name: '资产名称',
  warranty_expiry: '维保到期',
  days_remaining: '剩余天数',
  title: '标题',
  priority: '优先级',
}
