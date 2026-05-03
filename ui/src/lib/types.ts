// API response types. Hand-written for now; in M4+ we generate these
// from the Go contract.

export type Role = 'setup' | 'wifi-extender' | 'wifi-router';

export interface UplinkStatus {
	ssid: string;
	connected: boolean;
	rssi_dbm?: number;
}

export interface APStatus {
	ssid: string;
	up: boolean;
	clients: number;
}

export interface WANStatus {
	interface: string;
	mode?: string;
	up: boolean;
	ip?: string;
}

export interface NetworkStatus {
	backend: string;
	role: Role;
	uplink?: UplinkStatus;
	ap?: APStatus;
	wan?: WANStatus;
}

export interface SystemStatus {
	version: string;
	device: string;
	role: Role;
	auth_configured: boolean;
	network: NetworkStatus;
}

export interface ScannedNetwork {
	ssid: string;
	bssid?: string;
	channel: number;
	band: '2.4' | '5';
	rssi_dbm: number;
	secured: boolean;
}

export interface ScanResponse {
	networks: ScannedNetwork[];
}

/** Sidebar menu item declared by a plugin manifest. */
export interface PluginMenuItem {
	/** SPA route to navigate to (must start with `/`). */
	path: string;
	/** Visible label shown in the sidebar. */
	label: string;
	/** Bootstrap-icons class name (e.g. `bi-stars`). Optional. */
	icon?: string;
	/** Sort order within the plugins section (lower = earlier). */
	order?: number;
}

export interface Plugin {
	id: string;
	name: string;
	version: string;
	description?: string;
	enabled: boolean;
	/** Sidebar menu items contributed by this plugin (read from manifest). */
	menu?: PluginMenuItem[];
}

export interface PluginsResponse {
	plugins: Plugin[];
}

export interface Device {
	mac: string;
	label: string;
	hostname?: string;
	display_name?: string;
	ip?: string;
	online: boolean;
	lease_expires?: string;
	first_seen: string;
	last_seen: string;
	profile_id?: string;
}

export interface DevicesResponse {
	devices: Device[];
}

export interface BlockWindow {
	/** 0=Sunday … 6=Saturday */
	days: number[];
	/** "HH:MM" 24h */
	start: string;
	/** "HH:MM" 24h */
	end: string;
}

export interface Profile {
	id: string;
	name: string;
	description?: string;
	block_windows?: BlockWindow[];
	dns_blocklists?: string[];
	builtin: boolean;
}

export interface ProfilesResponse {
	profiles: Profile[];
}

export interface DNSTopBlocked {
	name: string;
	count: number;
}

export interface DNSSourceStats {
	url: string;
	last_fetch?: string;
	last_success?: string;
	last_error?: string;
	entries_added?: number;
}

export interface DNSStats {
	queries: number;
	blocked: number;
	blocked_ratio: number;
	top_blocked: DNSTopBlocked[];
	buffer_size: number;
	buffer_cap: number;
	blocklists: Record<string, number>;
	sources?: Record<string, DNSSourceStats>;
}

export interface DNSQuery {
	when: string;
	src_mac?: string;
	src_ip: string;
	qname: string;
	qtype: string;
	blocked: boolean;
	blocked_by?: string;
}

export interface DNSQueriesResponse {
	queries: DNSQuery[];
}

export interface TLSInfo {
	root_fingerprint: string;
	root_not_after: string;
	leaf_fingerprint: string;
	leaf_not_after: string;
	leaf_dns_names?: string[];
	leaf_ips?: string[];
}
