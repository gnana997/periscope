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

import {
  Navigate,
  Route,
  createBrowserRouter,
  createRoutesFromElements,
} from "react-router-dom";

import App from "./App";
import { AppShell, WithCluster } from "./routeShells";
import { FleetPage } from "./pages/FleetPage";

import { OverviewPage } from "./pages/OverviewPage";
import { AuditPage } from "./pages/AuditPage";
import { AuditEventDetailPage } from "./pages/AuditEventDetailPage";
import { ConfigMapsPage } from "./pages/ConfigMapsPage";
import { CronJobsPage } from "./pages/CronJobsPage";
import { EventsPage } from "./pages/EventsPage";
import { DaemonSetsPage } from "./pages/DaemonSetsPage";
import { DeploymentsPage } from "./pages/DeploymentsPage";
import { IngressesPage } from "./pages/IngressesPage";
import { JobsPage } from "./pages/JobsPage";
import { NamespacesPage } from "./pages/NamespacesPage";
import { NodesPage } from "./pages/NodesPage";
import { PodsPage } from "./pages/PodsPage";
import { SecretsPage } from "./pages/SecretsPage";
import { ServicesPage } from "./pages/ServicesPage";
import { StatefulSetsPage } from "./pages/StatefulSetsPage";
import { PVCsPage } from "./pages/PVCsPage";
import { PVsPage } from "./pages/PVsPage";
import { StorageClassesPage } from "./pages/StorageClassesPage";
import { RolesPage } from "./pages/RolesPage";
import { ClusterRolesPage } from "./pages/ClusterRolesPage";
import { RoleBindingsPage } from "./pages/RoleBindingsPage";
import { ClusterRoleBindingsPage } from "./pages/ClusterRoleBindingsPage";
import { ServiceAccountsPage } from "./pages/ServiceAccountsPage";
import { PodLogsPage } from "./pages/PodLogsPage";
import { DeploymentLogsPage } from "./pages/DeploymentLogsPage";
import { StatefulSetLogsPage } from "./pages/StatefulSetLogsPage";
import { DaemonSetLogsPage } from "./pages/DaemonSetLogsPage";
import { JobLogsPage } from "./pages/JobLogsPage";
import { HorizontalPodAutoscalersPage } from "./pages/HorizontalPodAutoscalersPage";
import { PodDisruptionBudgetsPage } from "./pages/PodDisruptionBudgetsPage";
import { ReplicaSetsPage } from "./pages/ReplicaSetsPage";
import { NetworkPoliciesPage } from "./pages/NetworkPoliciesPage";
import { IngressClassesPage } from "./pages/IngressClassesPage";
import { ResourceQuotasPage } from "./pages/ResourceQuotasPage";
import { LimitRangesPage } from "./pages/LimitRangesPage";
import { PriorityClassesPage } from "./pages/PriorityClassesPage";
import { RuntimeClassesPage } from "./pages/RuntimeClassesPage";
import { CRDsPage } from "./pages/CRDsPage";
import { CustomResourcesPage } from "./pages/CustomResourcesPage";
import { ExecPage } from "./pages/ExecPage";
import { HelmReleasesPage } from "./pages/HelmReleasesPage";
import { HelmReleasePage } from "./pages/HelmReleasePage";
import { HelmDiffPage } from "./pages/HelmDiffPage";

export const router = createBrowserRouter(
  createRoutesFromElements(
    <Route element={<App />}>
      <Route path="/" element={<FleetPage />} />
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
