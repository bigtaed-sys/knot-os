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
	network: NetworkStatus;
}
