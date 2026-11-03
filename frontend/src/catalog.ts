export interface Field {
  key: string
  label: string
  type?: 'select' | 'textarea' | 'password' | 'number'
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
}

const field = (key: string, label: string, required = false): Field => ({
  key,
  label,
  required
})

export const sections: Section[] = [
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
    subtitle: '管理物理站点与容量',
    api: '/datacenters',
    permission: 'datacenters:read',
    fields: [
      field('name', '机房名称', true),
      field('code', '编码'),
      field('location', '位置'),
      field('description', '描述')
    ]
  },
  {
    path: '/racks',
    title: '机柜管理',
    subtitle: '追踪机柜空间与部署位置',
    api: '/racks',
    permission: 'racks:read',
    fields: [
      field('name', '机柜名称', true),
      field('datacenter_id', '机房 ID'),
      { ...field('height_u', '高度 (U)'), type: 'number' },
      field('location', '位置')
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
      field('ip_address', '业务 IP'),
      field('mac_address', 'MAC 地址')
    ],
    columns: ['name', 'bmc_address', 'ip_address', 'status', 'project_id', 'created_at']
  },
  {
    path: '/images',
    title: '系统镜像',
    subtitle: '镜像仓库与安装介质',
    api: '/images',
    permission: 'images:read',
    fields: [],
    columns: ['name', 'size', 'created_at'],
    readonly: true
  },
  {
    path: '/installations',
    title: '装机任务',
    subtitle: '重装功能暂不支持；可查看任务中心已有记录',
    api: '/tasks',
    permission: 'tasks:read',
    fields: [],
    columns: ['kind', 'asset_id', 'status', 'error', 'created_at'],
    readonly: true
  },
  {
    path: '/subnets',
    title: 'IPAM · 子网',
    subtitle: '规划管理网络地址空间',
    api: '/subnets',
    permission: 'network:read',
    fields: [
      field('name', '名称', true),
      field('cidr', 'CIDR', true),
      field('gateway', '网关')
    ]
  },
  {
    path: '/ip-addresses',
    title: 'IP 地址',
    subtitle: '地址分配与占用追踪',
    api: '/ip-addresses',
    permission: 'network:read',
    fields: [
      field('address', 'IP 地址', true),
      { ...field('network_id', '子网 ID', true), type: 'number' },
      { ...field('asset_id', '资产 ID'), type: 'number' }
    ]
  },
  {
    path: '/pxe',
    title: 'PXE 配置',
    subtitle: '网络引导配置与模板',
    api: '/pxe-config',
    permission: 'network:read',
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
    fields: [
      field('name', '规则名称', true),
      field('metric', '监控指标', true),
      field('operator', '运算符', true),
      field('threshold', '阈值', true),
      field('severity', '告警等级', true),
      field('time_window', '时间窗口(秒)')
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
      { ...field('type', '渠道类型'), type: 'select', options: ['wechat', 'dingtalk', 'webhook', 'email'] },
      field('config', '配置(JSON)', true)
    ],
    columns: ['name', 'type', 'enabled', 'created_at']
  },
  // === 新增：Kickstart 装机模板 ===
  {
    path: '/kickstart-templates',
    title: '装机模板',
    subtitle: 'Kickstart 自动应答模板管理',
    api: '/kickstart-templates',
    permission: 'images:write',
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
    fields: [
      field('name', '地址池名称', true),
      field('network_id', '子网 ID', true),
      field('start_ip', '起始 IP', true),
      field('end_ip', '结束 IP', true),
      field('description', '描述')
    ],
    columns: ['name', 'start_ip', 'end_ip', 'network_id', 'created_at']
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
    permission: 'projects:write',
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
    columns: ['kind', 'asset_id', 'status', 'error', 'created_at'],
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
    columns: ['kind', 'asset_id', 'status', 'error', 'created_at'],
    readonly: true
  },
  {
    path: '/audit-logs',
    title: '审计日志',
    subtitle: '关键操作与访问轨迹',
    api: '/audit-logs',
    permission: 'audit:read',
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
    fields: [
      field('key', '配置项', true),
      field('value', '配置值', true),
      field('description', '描述')
    ],
    columns: ['key', 'value', 'description']
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
  datacenter_id: '机房 ID',
  rack_id: '机柜 ID',
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
  end_ip: '结束 IP'
}
