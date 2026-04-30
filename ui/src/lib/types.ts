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

export interface Plugin {
	id: string;
	name: string;
	version: string;
	description?: string;
	enabled: boolean;
}

export interface PluginsResponse {
	plugins: Plugin[];
}
