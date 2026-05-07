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
	stale: boolean;
	lease_expires?: string;
	first_seen: string;
	last_seen: string;
	last_arp_seen?: string;
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
	route_via?: string;
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

export interface ReleaseInfo {
	tag: string;
	name?: string;
	published_at: string;
	notes?: string;
	binary_url: string;
	signature_url: string;
	binary_size: number;
	has_signature: boolean;
}

export interface UpdateCheckResult {
	current_version: string;
	latest_version: string;
	update_available: boolean;
	signing_enabled: boolean;
	latest?: ReleaseInfo;
}

export interface RescueInfo {
	public_key: string;
	private_available: boolean;
}

export interface RescueRevealed {
	private_key: string;
	public_key: string;
	warning: string;
}

export interface NotifyLinkedChat {
	chat_id: number;
	username?: string;
	first_name?: string;
	last_name?: string;
	lang: 'ru' | 'en' | string;
	linked_at: string;
}

export interface NotifyState {
	bot_configured: boolean;
	bot_username?: string;
	primary_lang: 'ru' | 'en' | string;
	chats: NotifyLinkedChat[];
}

export interface NotifyPIN {
	pin: string;
	expires_at: string;
}

export interface NotifyPINStatus {
	active: boolean;
	expires_at?: string;
}

export interface ChannelLoad {
	channel: number;
	networks: number;
	score: number;
	recommended?: boolean;
}

export interface ChannelReport {
	band: '2.4' | string;
	channels: ChannelLoad[];
	recommended: number;
	current_channel?: number;
}

export interface DNSUpstream {
	mode: 'udp' | 'doh' | string;
	upstreams: string[];
	defaults: string[];
}

export interface GuestSession {
	ssid: string;
	psk: string;
	created_at: string;
	expires_at?: string;
	remaining_sec: number;
	profile_id?: string;
	wifi_qr: string;
	qr_png_base64: string;
}

// --- VPN (WireGuard road-warrior) -----------------------------------------

export interface VPNServer {
	enabled: boolean;
	listen_port: number;
	interface_cidr: string;
	endpoint_host: string;
	public_key: string;
	peer_count: number;
}

export interface VPNPeer {
	id: string;
	name: string;
	public_key: string;
	allowed_ip: string;
	profile_id?: string;
	created_at: string;
	last_handshake?: string;
}

export interface VPNPeersResponse {
	peers: VPNPeer[];
}

export interface VPNAddPeerResponse {
	peer: VPNPeer;
	client_config: string;
	private_key: string;
	qr_png_base64: string;
}


// --- VPN subscriptions (M27/M28/M29) ---------------------------------------

export interface SubscriptionServer {
	id: string;
	display_name: string;
	uri: string;
	outbound: {
		tag?: string;
		type: string;
		display_name?: string;
		server?: string;
		port?: number;
	};
}

export interface Subscription {
	id: string;
	display_name: string;
	url?: string;
	user_agent?: string;
	servers: SubscriptionServer[];
	last_fetched?: string;
	last_error?: string;
}

export interface SubscriptionsResponse {
	subscriptions: Subscription[];
}

export interface SubscriptionRefreshResponse {
	subscription: Subscription;
	subscription_userinfo?: string;
	profile_title?: string;
	parse_warnings?: string[];
}

export interface RoutingDevice {
	mac: string;
	outbound: string;
	status: 'direct' | 'tunnel' | 'kill';
	profile_id: string;
}

export interface RoutingResponse {
	devices: RoutingDevice[];
	missing_outbounds: string[];
}
