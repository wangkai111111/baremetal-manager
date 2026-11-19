export const relationResources: Record<string, { path: string; label: string }> = {
  asset_id: { path: '/assets', label: '服务器' },
  datacenter_id: { path: '/datacenters', label: '机房' },
  department_id: { path: '/departments', label: '部门' },
  parent_id: { path: '/departments', label: '上级部门' },
  network_id: { path: '/networks', label: '子网' },
  image_id: { path: '/images', label: '系统镜像' },
  image_template_id: { path: '/image-templates', label: '镜像模板' },
  kickstart_template_id: { path: '/kickstart-templates', label: '应答模板' },
  partition_template_id: { path: '/partition-templates', label: '分区模板' },
  install_plan_id: { path: '/install-plans', label: '安装方案' },
  firmware_package_id: { path: '/firmware-packages', label: '固件包' },
  user_id: { path: '/members', label: '用户' }
}
