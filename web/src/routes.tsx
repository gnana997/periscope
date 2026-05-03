// routes — react-router data-router config.
//
// Page-component imports + the route tree live here. Layout
// components used as route elements (App, AppShell, WithCluster,
// RootRedirect) are imported — defining them in the same file as
// the `router` data export trips Vite's react-refresh rule.
//
// Routes are written as JSX via createRoutesFromElements so the
// structure reads the same way it did under <BrowserRouter> +
// <Routes>; the difference is the router instance is created
// up-front and passed to RouterProvider in main.tsx, which gives us
// access to `useBlocker` (used by YamlEditor for the unsaved-changes
// guard on cross-page navigation).

import { Suspense } from "react";
import {
  Navigate,
  Route,
  createBrowserRouter,
  createRoutesFromElements,
} from "react-router-dom";

import App from "./App";
import { AppShell, WithCluster } from "./routeShells";
import { lazyNamed } from "./lib/lazyNamed";
import { LoadingState } from "./components/table/states";

const FleetPage = lazyNamed(() => import("./pages/FleetPage"), "FleetPage");

const OverviewPage = lazyNamed(() => import("./pages/OverviewPage"), "OverviewPage");
const AuditPage = lazyNamed(() => import("./pages/AuditPage"), "AuditPage");
const AuditEventDetailPage = lazyNamed(() => import("./pages/AuditEventDetailPage"), "AuditEventDetailPage");
const ConfigMapsPage = lazyNamed(() => import("./pages/ConfigMapsPage"), "ConfigMapsPage");
const CronJobsPage = lazyNamed(() => import("./pages/CronJobsPage"), "CronJobsPage");
const EventsPage = lazyNamed(() => import("./pages/EventsPage"), "EventsPage");
const DaemonSetsPage = lazyNamed(() => import("./pages/DaemonSetsPage"), "DaemonSetsPage");
const DeploymentsPage = lazyNamed(() => import("./pages/DeploymentsPage"), "DeploymentsPage");
const IngressesPage = lazyNamed(() => import("./pages/IngressesPage"), "IngressesPage");
const JobsPage = lazyNamed(() => import("./pages/JobsPage"), "JobsPage");
const NamespacesPage = lazyNamed(() => import("./pages/NamespacesPage"), "NamespacesPage");
const NodesPage = lazyNamed(() => import("./pages/NodesPage"), "NodesPage");
const PodsPage = lazyNamed(() => import("./pages/PodsPage"), "PodsPage");
const SecretsPage = lazyNamed(() => import("./pages/SecretsPage"), "SecretsPage");
const ServicesPage = lazyNamed(() => import("./pages/ServicesPage"), "ServicesPage");
const StatefulSetsPage = lazyNamed(() => import("./pages/StatefulSetsPage"), "StatefulSetsPage");
const PVCsPage = lazyNamed(() => import("./pages/PVCsPage"), "PVCsPage");
const PVsPage = lazyNamed(() => import("./pages/PVsPage"), "PVsPage");
const StorageClassesPage = lazyNamed(() => import("./pages/StorageClassesPage"), "StorageClassesPage");
const RolesPage = lazyNamed(() => import("./pages/RolesPage"), "RolesPage");
const ClusterRolesPage = lazyNamed(() => import("./pages/ClusterRolesPage"), "ClusterRolesPage");
const RoleBindingsPage = lazyNamed(() => import("./pages/RoleBindingsPage"), "RoleBindingsPage");
const ClusterRoleBindingsPage = lazyNamed(() => import("./pages/ClusterRoleBindingsPage"), "ClusterRoleBindingsPage");
const ServiceAccountsPage = lazyNamed(() => import("./pages/ServiceAccountsPage"), "ServiceAccountsPage");
const PodLogsPage = lazyNamed(() => import("./pages/PodLogsPage"), "PodLogsPage");
const DeploymentLogsPage = lazyNamed(() => import("./pages/DeploymentLogsPage"), "DeploymentLogsPage");
const StatefulSetLogsPage = lazyNamed(() => import("./pages/StatefulSetLogsPage"), "StatefulSetLogsPage");
const DaemonSetLogsPage = lazyNamed(() => import("./pages/DaemonSetLogsPage"), "DaemonSetLogsPage");
const JobLogsPage = lazyNamed(() => import("./pages/JobLogsPage"), "JobLogsPage");
const HorizontalPodAutoscalersPage = lazyNamed(() => import("./pages/HorizontalPodAutoscalersPage"), "HorizontalPodAutoscalersPage");
const PodDisruptionBudgetsPage = lazyNamed(() => import("./pages/PodDisruptionBudgetsPage"), "PodDisruptionBudgetsPage");
const ReplicaSetsPage = lazyNamed(() => import("./pages/ReplicaSetsPage"), "ReplicaSetsPage");
const NetworkPoliciesPage = lazyNamed(() => import("./pages/NetworkPoliciesPage"), "NetworkPoliciesPage");
const IngressClassesPage = lazyNamed(() => import("./pages/IngressClassesPage"), "IngressClassesPage");
const ResourceQuotasPage = lazyNamed(() => import("./pages/ResourceQuotasPage"), "ResourceQuotasPage");
const LimitRangesPage = lazyNamed(() => import("./pages/LimitRangesPage"), "LimitRangesPage");
const PriorityClassesPage = lazyNamed(() => import("./pages/PriorityClassesPage"), "PriorityClassesPage");
const RuntimeClassesPage = lazyNamed(() => import("./pages/RuntimeClassesPage"), "RuntimeClassesPage");
const CRDsPage = lazyNamed(() => import("./pages/CRDsPage"), "CRDsPage");
const CustomResourcesPage = lazyNamed(() => import("./pages/CustomResourcesPage"), "CustomResourcesPage");
const ExecPage = lazyNamed(() => import("./pages/ExecPage"), "ExecPage");
const HelmReleasesPage = lazyNamed(() => import("./pages/HelmReleasesPage"), "HelmReleasesPage");
const HelmReleasePage = lazyNamed(() => import("./pages/HelmReleasePage"), "HelmReleasePage");
const HelmDiffPage = lazyNamed(() => import("./pages/HelmDiffPage"), "HelmDiffPage");

export const router = createBrowserRouter(
  createRoutesFromElements(
    <Route element={<App />}>
      <Route path="/" element={<Suspense fallback={<LoadingState resource="page" />}><FleetPage /></Suspense>} />
      <Route path="/clusters/:cluster" element={<AppShell />}>
        <Route index element={<Navigate to="overview" replace />} />
        <Route path="overview" element={<WithCluster Page={OverviewPage} />} />
        <Route path="audit" element={<AuditPage />} />
        <Route path="audit/:eventId" element={<AuditEventDetailPage />} />
        <Route path="pods" element={<WithCluster Page={PodsPage} />} />
        <Route path="deployments" element={<WithCluster Page={DeploymentsPage} />} />
        <Route path="statefulsets" element={<WithCluster Page={StatefulSetsPage} />} />
        <Route path="daemonsets" element={<WithCluster Page={DaemonSetsPage} />} />
        <Route path="jobs" element={<WithCluster Page={JobsPage} />} />
        <Route path="cronjobs" element={<WithCluster Page={CronJobsPage} />} />
        <Route path="services" element={<WithCluster Page={ServicesPage} />} />
        <Route path="ingresses" element={<WithCluster Page={IngressesPage} />} />
        <Route path="configmaps" element={<WithCluster Page={ConfigMapsPage} />} />
        <Route path="secrets" element={<WithCluster Page={SecretsPage} />} />
        <Route path="nodes" element={<WithCluster Page={NodesPage} />} />
        <Route path="namespaces" element={<WithCluster Page={NamespacesPage} />} />
        <Route path="events" element={<WithCluster Page={EventsPage} />} />
        <Route path="pvcs" element={<WithCluster Page={PVCsPage} />} />
        <Route path="pvs" element={<WithCluster Page={PVsPage} />} />
        <Route path="storageclasses" element={<WithCluster Page={StorageClassesPage} />} />
        <Route path="roles" element={<WithCluster Page={RolesPage} />} />
        <Route path="clusterroles" element={<WithCluster Page={ClusterRolesPage} />} />
        <Route path="rolebindings" element={<WithCluster Page={RoleBindingsPage} />} />
        <Route path="clusterrolebindings" element={<WithCluster Page={ClusterRoleBindingsPage} />} />
        <Route path="serviceaccounts" element={<WithCluster Page={ServiceAccountsPage} />} />
        <Route path="horizontalpodautoscalers" element={<WithCluster Page={HorizontalPodAutoscalersPage} />} />
        <Route path="poddisruptionbudgets" element={<WithCluster Page={PodDisruptionBudgetsPage} />} />
        <Route path="replicasets" element={<WithCluster Page={ReplicaSetsPage} />} />
        <Route path="networkpolicies" element={<WithCluster Page={NetworkPoliciesPage} />} />
        <Route path="ingressclasses" element={<WithCluster Page={IngressClassesPage} />} />
        <Route path="resourcequotas" element={<WithCluster Page={ResourceQuotasPage} />} />
        <Route path="limitranges" element={<WithCluster Page={LimitRangesPage} />} />
        <Route path="priorityclasses" element={<WithCluster Page={PriorityClassesPage} />} />
        <Route path="runtimeclasses" element={<WithCluster Page={RuntimeClassesPage} />} />
        <Route path="crds" element={<WithCluster Page={CRDsPage} />} />
        <Route path="customresources/:group/:version/:plural" element={<WithCluster Page={CustomResourcesPage} />} />
        <Route path="pods/:ns/:name/logs" element={<WithCluster Page={PodLogsPage} />} />
        <Route path="deployments/:ns/:name/logs" element={<WithCluster Page={DeploymentLogsPage} />} />
        <Route path="statefulsets/:ns/:name/logs" element={<WithCluster Page={StatefulSetLogsPage} />} />
        <Route path="daemonsets/:ns/:name/logs" element={<WithCluster Page={DaemonSetLogsPage} />} />
        <Route path="jobs/:ns/:name/logs" element={<WithCluster Page={JobLogsPage} />} />
        <Route path="pods/:ns/:name/exec" element={<WithCluster Page={ExecPage} />} />
        <Route path="helm" element={<WithCluster Page={HelmReleasesPage} />} />
        <Route path="helm/:namespace/:name" element={<HelmReleasePage />} />
        <Route path="helm/:namespace/:name/diff" element={<HelmDiffPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Route>,
  ),
);
