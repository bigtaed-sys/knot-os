// API response types. Hand-written for now; in M4+ we generate these
// from the Go contract.

export type Role = 'setup' | 'wifi-extender';

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

export interface NetworkStatus {
	backend: string;
	role: Role;
	uplink?: UplinkStatus;
	ap?: APStatus;
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
