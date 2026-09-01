/**
 * Icon mapping shared by every place that shows a resource kind: the
 * navigator tree and the cross-kind power search dropdown.
 *
 * Kept in one module so the two surfaces can never drift — a Deployment
 * icon in the sidebar that does not match the one in search results would
 * undermine the whole point of using icons as a recognition aid.
 */
import type { ResourceKind } from '$lib/api/client'
import {
  Box,
  Boxes,
  Container,
  Server,
  Network,
  Shield,
  Key,
  Settings,
  Layers,
  HardDrive,
  Globe,
  FileText,
  Package,
  Database,
  Zap,
  Lock,
  Users,
  Link,
  Webhook,
  Activity,
  Gauge,
  CircleDot,
  FolderOpen,
  Route,
  Scale,
  Waypoints,
  Timer,
  Repeat,
  SquareStack,
  Puzzle,
  type LucideIcon,
} from '@lucide/svelte'

export type { LucideIcon }

/**
 * Maps category names to icons.
 *
 * Deliberately monochrome — every icon here and in `ICON_BY_KIND_NAME` below
 * renders in the same neutral tone. A colour-coded category was tried and
 * dropped: it read as a status signal (as colour does everywhere else in the
 * app) rather than as mere decoration, which made an all-green Networking
 * section look "healthy" and a red Access Control section look "wrong" for
 * no reason at all.
 */
export const CATEGORY_META: Record<string, { icon: LucideIcon }> = {
  Workloads: { icon: Boxes },
  Networking: { icon: Network },
  Storage: { icon: HardDrive },
  Configuration: { icon: Settings },
  'Access Control': { icon: Shield },
  'Custom Resources': { icon: Puzzle },
}

/** Fallback for categories the backend reports that we have no icon for. */
export const DEFAULT_CATEGORY_META = { icon: Package }

export function categoryMeta(category: string): { icon: LucideIcon } {
  return CATEGORY_META[category] ?? DEFAULT_CATEGORY_META
}

/** Maps a Kubernetes kind name to a specific icon. */
const ICON_BY_KIND_NAME: Record<string, LucideIcon> = {
  Pod: Box,
  Deployment: Layers,
  StatefulSet: Database,
  // Boxes, not Container: the dependency map draws a pod's CONTAINERS with
  // lucide's container glyph, and a DaemonSet wearing the same one made two
  // unrelated things look identical wherever they appeared together. A set of
  // boxes also says what a DaemonSet is — one pod on every node — against the
  // single Box a Pod gets.
  DaemonSet: Boxes,
  ReplicaSet: Repeat,
  Job: Zap,
  CronJob: Timer,
  Service: Globe,
  Ingress: Route,
  NetworkPolicy: Shield,
  EndpointSlice: Waypoints,
  ConfigMap: FileText,
  Secret: Lock,
  PersistentVolumeClaim: HardDrive,
  PersistentVolume: HardDrive,
  StorageClass: SquareStack,
  Node: Server,
  Namespace: FolderOpen,
  ServiceAccount: Users,
  Role: Key,
  ClusterRole: Key,
  RoleBinding: Link,
  ClusterRoleBinding: Link,
  HorizontalPodAutoscaler: Scale,
  Event: Activity,
  LimitRange: Gauge,
  ResourceQuota: Gauge,
  PodDisruptionBudget: Shield,
  MutatingWebhookConfiguration: Webhook,
  ValidatingWebhookConfiguration: Webhook,
  CustomResourceDefinition: Puzzle,
  IngressClass: Route,
  Endpoints: CircleDot,
}

/** Returns the icon for a resource kind, falling back to a generic package. */
export function iconForKind(kind: Pick<ResourceKind, 'kind'>): LucideIcon {
  return ICON_BY_KIND_NAME[kind.kind] ?? Package
}
