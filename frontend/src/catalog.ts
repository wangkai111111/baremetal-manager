export interface Field {
  key: string
  label: string
  type?: 'select' | 'textarea' | 'password' | 'number' | 'json'
  options?: string[]
  required?: boolean
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
  mode?: 'both' | 'self_use' | 'external_operation'
}

const field = (key: string, label: string, required = false): Field => ({
  key,
  label,
  required
})

export const sections: Section[] = [
  { path: '/tenants', title: '租户管理', subtitle: '组织租户访问边界', api: '/tenants', permission: 'projects:read', mode: 'both', fields: [field('name', '租户名称', true), field('code', '编码', true), field('description', '描述')], columns: ['name', 'code', 'description', 'created_at'] },
  {
    path: '/projects',
    title: '项目管理',
    subtitle: '组织业务资源与访问边界',
    api: '/projects',
    permission: 'projects:read',
    mode: 'both',
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
    mode: 'both',
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
    mode: 'both',
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
    title: '系统镜像',
    subtitle: '镜像仓库与安装介质',
    api: '/images',
    permission: 'images:read',
    mode: 'both',
    fields: [field('name', '镜像名称', true), field('os_type', '操作系统', true), field('version', '版本', true), field('architecture', '架构', true), field('object_key', '存储对象键', true), { ...field('size_bytes', '大小（字节）', true), type: 'number' }, field('sha256', 'SHA256', true)],
    columns: ['name', 'os_type', 'version', 'status', 'created_at']
  },
  {
    path: '/installations',
    title: '装机任务',
    subtitle: '重装功能暂不支持；可查看任务中心已有记录',
    api: '/tasks',
    permission: 'tasks:read',
    mode: 'both',
    fields: [],
    columns: ['kind', 'asset_id', 'status', 'error', 'created_at'],
    readonly: true
  },
  {
    path: '/subnets',
    title: 'IPAM · 子网',
    subtitle: '规划管理网络地址空间',
    api: '/networks',
    permission: 'network:read',
    mode: 'both',
    fields: [
      field('name', '名称', true),
      field('cidr', 'CIDR', true),
      field('gateway', '网关'),
      field('dns_servers', 'DNS 服务器')
    ],
    columns: ['name', 'cidr', 'gateway', 'dns_servers', 'created_at']
  },
  {
    path: '/ip-addresses',
    title: 'IP 地址',
    subtitle: '地址分配与占用追踪',
    api: '/ip-allocations',
    permission: 'network:read',
    mode: 'both',
    fields: [
      field('address', 'IP 地址', true),
      { ...field('network_id', '子网 ID', true), type: 'number' },
      { ...field('asset_id', '资产 ID'), type: 'number' }
    ],
    columns: ['address', 'network_id', 'asset_id', 'created_at']
  },
  {
    path: '/pxe',
    title: 'PXE 配置',
    subtitle: '网络引导配置与模板',
    api: '/pxe-config',
    permission: 'network:read',
    mode: 'both',
    fields: [
      field('name', '配置名称', true),
      field('boot_file', '引导文件'),
      field('kernel', '内核'),
      field('initrd', 'Initrd'),
      field('append', '启动参数')
    ]
  },
  {
    path: '/alerts',
    title: '监控告警',
    subtitle: '查看事件、异常与处理进度',
    api: '/alerts',
    permission: 'alerts:read',
    mode: 'both',
    fields: [],
    columns: ['severity', 'message', 'asset_id', 'resolved', 'created_at'],
    readonly: true
  },
  // === 新增：告警规则 ===
  {
    path: '/alert-rules',
    title: '告警规则',
    subtitle: '自定义监控阈值与告警策略',
    api: '/alert-rules',
    permission: 'alerts:manage',
    mode: 'both',
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
    mode: 'both',
    fields: [
      field('name', '渠道名称', true),
      { ...field('kind', '渠道类型', true), type: 'select', options: ['email', 'webhook', 'lark'] },
      { ...field('config', '非敏感配置(JSON)', true), type: 'json' },
      { ...field('secret', 'SMTP 密码或 HTTPS Webhook 完整地址'), type: 'password' }
    ],
    columns: ['name', 'kind', 'enabled', 'last_test_status', 'last_test_at', 'last_test_message', 'created_at']
  },
  // === 新增：Kickstart 装机模板 ===
  {
    path: '/kickstart-templates',
    title: '装机模板',
    subtitle: 'Kickstart 自动应答模板管理',
    api: '/kickstart-templates',
    permission: 'images:write',
    mode: 'both',
    fields: [
      field('name', '模板名称', true),
      field('os_type', '操作系统', true),
      field('template_content', '模板内容', true),
      field('partition_scheme', '分区方案'),
      field('post_script', '装机后脚本')
    ],
    columns: ['name', 'os_type', 'created_at']
  },
  // === 新增：IP 地址池 ===
  {
    path: '/ip-pools',
    title: 'IP 地址池',
    subtitle: '管理 IP 地址分配池',
    api: '/ip-pools',
    permission: 'network:write',
    mode: 'both',
    fields: [
      field('name', '地址池名称', true),
      { ...field('network_id', '子网 ID', true), type: 'number' },
      field('cidr', 'CIDR', true),
      field('gateway', '网关')
    ],
    columns: ['name', 'network_id', 'cidr', 'gateway', 'created_at']
  },
  // === 新增：标签管理 ===
  {
    path: '/tags',
    title: '标签管理',
    subtitle: '自定义标签分类与筛选',
    api: '/tags',
    permission: 'assets:write',
    mode: 'both',
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
    mode: 'both',
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
    permission: 'projects:write',
    mode: 'both',
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
    mode: 'both',
    fields: [],
    columns: ['kind', 'asset_id', 'status', 'error', 'created_at'],
    readonly: true
  },
  {
    path: '/users',
    title: '用户管理',
    subtitle: '用户账户与访问控制',
    api: '/users',
    permission: 'iam:read',
    mode: 'both',
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
    mode: 'both',
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
    mode: 'both',
    fields: [],
    columns: ['kind', 'asset_id', 'status', 'error', 'created_at'],
    readonly: true
  },
  {
    path: '/audit-logs',
    title: '审计日志',
    subtitle: '关键操作与访问轨迹',
    api: '/audit',
    permission: 'audit:read',
    mode: 'both',
    fields: [],
    columns: ['user_id', 'action', 'target', 'created_at'],
    readonly: true
  },
  {
    path: '/settings',
    title: '系统设置',
    subtitle: '平台参数与集成配置',
    api: '/settings',
    permission: 'settings:read',
    mode: 'both',
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
    mode: 'self_use',
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
    mode: 'self_use',
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
    mode: 'self_use',
    fields: [],
    columns: ['asset_id', 'name', 'warranty_expiry', 'days_remaining', 'status'],
    readonly: true
  },
  // === 对外运营模式专属：产品管理 ===
  {
    path: '/products',
    title: '产品管理',
    subtitle: '定义可售产品与规格',
    api: '/products',
    permission: 'products:read',
    mode: 'external_operation',
    fields: [
      field('name', '产品名称', true),
      field('description', '描述'),
      { ...field('spec_json', '规格配置'), type: 'json' },
      { ...field('pricing_json', '定价方案'), type: 'json' },
      { ...field('billing_cycle', '计费周期'), type: 'select', options: ['hourly', 'daily', 'monthly', 'yearly'] },
      { ...field('status', '状态'), type: 'select', options: ['active', 'inactive', 'sold_out'] }
    ],
    columns: ['name', 'billing_cycle', 'status', 'created_at']
  },
  // === 对外运营模式专属：订单管理 ===
  // 只读护栏：后端无 PUT /orders/:id 与 DELETE /orders/:id，写操作一律走
  // POST /orders（下单）、PUT /orders/:id/:transition（流转）与 POST /orders/:id/pay-balance，
  // 页面由专用 OrdersView 承载，ResourceView 仍需 readonly 以免命中 404。
  {
    path: '/orders',
    title: '订单管理',
    subtitle: '跟踪订单生命周期',
    api: '/orders',
    permission: 'orders:read',
    mode: 'external_operation',
    fields: [
      { ...field('type', '订单类型'), type: 'select', options: ['purchase', 'renew', 'upgrade', 'refund'] },
      { ...field('product_id', '产品 ID'), type: 'number' },
      { ...field('asset_id', '资产 ID'), type: 'number' },
      { ...field('amount', '金额'), type: 'number' },
      { ...field('status', '状态'), type: 'select', options: ['pending', 'paid', 'processing', 'completed', 'refunded', 'cancelled'] }
    ],
    columns: ['type', 'product_id', 'asset_id', 'amount', 'status', 'created_at'],
    readonly: true
  },
  // === 对外运营模式专属：账单管理 ===
  // 只读护栏：后端仅有 GET /bills、GET /bills/:id、GET /bills/:id/export 与 POST /bills/generate，
  // 不存在 POST/PUT/DELETE /bills/:id。
  {
    path: '/bills',
    title: '账单管理',
    subtitle: '周期性账单生成与追踪',
    api: '/bills',
    permission: 'billing:read',
    mode: 'external_operation',
    fields: [
      field('period_start', '账期开始', true),
      field('period_end', '账期结束', true),
      { ...field('total_amount', '总金额'), type: 'number' },
      { ...field('status', '状态'), type: 'select', options: ['unpaid', 'paid', 'overdue'] }
    ],
    columns: ['period_start', 'period_end', 'total_amount', 'status', 'due_date', 'created_at'],
    readonly: true
  },
  // === 对外运营模式专属：账户管理 ===
  // 只读护栏：GET /accounts 返回单个账户对象（无 /accounts/:id），写入口仅有
  // POST /accounts/recharge，因此不得出现新建/编辑/删除。
  // 列与 accounts 表实际字段对齐（无 name / currency / status 列）。
  {
    path: '/accounts',
    title: '账户管理',
    subtitle: '租户账户余额与信用额度',
    api: '/accounts',
    permission: 'finance:read',
    mode: 'external_operation',
    fields: [
      { ...field('balance', '可用余额'), type: 'number' },
      { ...field('frozen_amount', '冻结金额'), type: 'number' },
      { ...field('credit_limit', '信用额度'), type: 'number' }
    ],
    columns: ['balance', 'frozen_amount', 'credit_limit', 'created_at', 'updated_at'],
    readonly: true
  },
  // === 对外运营模式专属：支付记录 ===
  // 只读护栏：后端仅有 GET /payments 与 POST /payments，无 GET/:id、PUT、DELETE。
  // method 枚举含 010 迁移扩展的 balance（余额支付流水）。
  {
    path: '/payments',
    title: '支付记录',
    subtitle: '充值与余额支付流水对账',
    api: '/payments',
    permission: 'finance:read',
    mode: 'external_operation',
    fields: [
      { ...field('amount', '金额', true), type: 'number' },
      { ...field('method', '支付方式'), type: 'select', options: ['alipay', 'wechat', 'transfer', 'balance'] },
      { ...field('status', '状态'), type: 'select', options: ['pending', 'success', 'failed'] }
    ],
    columns: ['amount', 'method', 'status', 'transaction_id', 'paid_at', 'created_at'],
    readonly: true
  },
  // === 对外运营模式专属：发票管理 ===
  // 只读护栏：后端无 GET/PUT/DELETE /invoices/:id，仅有 POST /invoices 与
  // PUT /invoices/:id/:status 流转；页面由专用 InvoiceView 承载。
  {
    path: '/invoices',
    title: '发票管理',
    subtitle: '发票申请与开具',
    api: '/invoices',
    permission: 'finance:read',
    mode: 'external_operation',
    fields: [
      { ...field('type', '发票类型'), type: 'select', options: ['normal', 'special'] },
      field('title', '发票抬头', true),
      field('tax_number', '税号', true),
      { ...field('amount', '金额'), type: 'number' },
      { ...field('status', '状态'), type: 'select', options: ['requested', 'issued', 'cancelled'] }
    ],
    columns: ['type', 'title', 'tax_number', 'amount', 'status', 'created_at'],
    readonly: true
  },
  // === 对外运营模式专属：优惠券 ===
  // CRUD 契约完整（POST/PUT/DELETE /coupons、POST /coupons/:code/redeem），保持可写。
  {
    path: '/coupons',
    title: '优惠券',
    subtitle: '折扣码与优惠活动',
    api: '/coupons',
    permission: 'marketing:read',
    mode: 'external_operation',
    fields: [
      field('code', '优惠码', true),
      field('name', '活动名称', true),
      { ...field('type', '优惠类型'), type: 'select', options: ['fixed', 'percent'] },
      { ...field('value', '优惠值'), type: 'number' },
      field('valid_from', '生效时间'),
      field('valid_until', '失效时间')
    ],
    columns: ['code', 'name', 'type', 'value', 'valid_from', 'valid_until', 'status']
  },
  // === 两种模式通用：工单系统（后端 work_orders 路由在 self_use / external_operation 下均开放） ===
  {
    path: '/work-orders',
    title: '工单管理',
    subtitle: '服务请求与故障处理',
    api: '/work-orders',
    permission: 'work_orders:read',
    mode: 'both',
    fields: [
      { ...field('type', '工单类型'), type: 'select', options: ['fault', 'technical', 'consultation', 'other'] },
      field('title', '标题', true),
      { ...field('description', '描述'), type: 'textarea' },
      { ...field('status', '状态'), type: 'select', options: ['open', 'in_progress', 'resolved', 'closed'] },
      { ...field('priority', '优先级'), type: 'select', options: ['low', 'medium', 'high', 'urgent'] }
    ],
    columns: ['type', 'title', 'status', 'priority', 'created_at']
  },
  // === 对外运营模式专属：运营数据大盘 ===
  {
    path: '/operations-dashboard',
    title: '运营数据大盘',
    subtitle: '全局运营指标与趋势分析',
    api: '/admin/operations-dashboard',
    permission: 'operations:read',
    mode: 'external_operation',
    fields: [],
    columns: [],
    readonly: true
  }
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
  tenant_id: '租户 ID',
  mac_address: 'MAC 地址',
  size: '大小 (字节)',
  kind: '任务类型',
  resolved: '已解决',
  target: '目标',
  user_id: '用户 ID',
  network_id: '子网 ID',
  error: '错误',
  serial_number: '序列号',
  bmc_ip: 'BMC IP',
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
  boot_file: '引导文件',
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
  method: '折旧方式',
  useful_life_months: '使用年限（月）',
  residual_value: '残值',
  asset_name: '资产名称',
  warranty_expiry: '维保到期',
  days_remaining: '剩余天数',
  spec_json: '规格配置',
  pricing_json: '定价方案',
  billing_cycle: '计费周期',
  product_id: '产品 ID',
  period_start: '账期开始',
  period_end: '账期结束',
  total_amount: '总金额',
  due_date: '到期日',
  balance: '余额',
  frozen_amount: '冻结金额',
  credit_limit: '信用额度',
  currency: '币种',
  transaction_id: '交易号',
  paid_at: '支付时间',
  issued_at: '开具时间',
  valid_from: '生效时间',
  valid_until: '失效时间',
  title: '标题',
  tax_number: '税号',
  priority: '优先级',
  amount: '金额'
}

// === 按模式过滤 Section ===
export function getSectionsByMode(mode: 'self_use' | 'external_operation' | 'both'): Section[] {
  return sections.filter(s => {
    const sectionMode = s.mode || 'both'
    if (sectionMode === 'both') return true
    return sectionMode === mode
  })
}
