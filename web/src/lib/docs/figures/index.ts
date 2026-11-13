import type { Component } from 'svelte';
import SitesFlow from './SitesFlow.svelte';
import SecurityBoundary from './SecurityBoundary.svelte';
import LimitsFigure from './LimitsFigure.svelte';
import WidgetSample from './WidgetSample.svelte';
import ReleaseNotes from './ReleaseNotes.svelte';
import SystemMap from './SystemMap.svelte';
import ServicesNet from './ServicesNet.svelte';
import StackNet from './StackNet.svelte';
import PortsFlow from './PortsFlow.svelte';
import Releases from './Releases.svelte';
import RestoreFlow from './RestoreFlow.svelte';
import OidcFlow from './OidcFlow.svelte';
import EnrollFlow from './EnrollFlow.svelte';

/** Figures a guide page can place with <!-- figure: name -->. */
export const figures: Record<string, Component> = {
	'sites-flow': SitesFlow,
	'security-boundary': SecurityBoundary,
	'limits-legend': LimitsFigure,
	'widget-sample': WidgetSample,
	'release-notes': ReleaseNotes,
	'system-map': SystemMap,
	'services-network': ServicesNet,
	'stack-network': StackNet,
	'game-ports': PortsFlow,
	'site-releases': Releases,
	'app-restore': RestoreFlow,
	'sso-flow': OidcFlow,
	'node-enrollment': EnrollFlow
};
