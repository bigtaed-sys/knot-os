// API response types. Hand-written for now; in M4+ we generate these
// from the Go contract.

export type Role = 'setup' | 'wifi-extender' | 'wifi-router';

// --- Setup wizard capability detection ----------------------------

export interface EthAdapter {
	name: string;
	driver: string;
	link: boolean;
	usb: boolean;
	usb_vendor?: string;
	usb_product?: string;
	model: string;
}

export interface CapabilityReport {
	pi: string;
	pi_model_string?: string;
	eth: EthAdapter[];
	guest_ap_capable: boolean;
	router_capable: boolean;
}

// PortView is one Ethernet port plus its role under the active config
// ("wan" | "lan" | "unused"), as returned by GET /network.
export interface PortView extends EthAdapter {
	role: 'wan' | 'lan' | 'unused';
}

// --- /network settings (post-setup network editor) -------------------

export interface ModemSettings {
	apn?: string;
	pin?: string;
	username?: string;
	sim_slot?: number;
	data_limit_mb?: number;
	cycle_reset_day?: number;
}

export interface WANConfig {
	mode?: 'dhcp' | 'modem';
	interface?: string;
	modem?: ModemSettings;
}

export interface APConfig {
	ssid?: string;
	psk?: string;
	band?: '2.4' | '5';
	channel?: number;
}

export interface UplinkConfig {
	ssid?: string;
	psk?: string;
}

export interface LANConfig {
	cidr?: string;
	dhcp?: { pool_start?: string; pool_end?: string };
}

// NetworkConfig is the subset of config.Network the settings page edits
// (the daemon carries more — dns/zapret/etc. — untouched here).
export interface NetworkConfig {
	wan?: WANConfig;
	uplink?: UplinkConfig;
	ap?: APConfig;
	lan?: LANConfig;
	lan_ports?: string[];
}

export interface NetworkResponse {
	role: 'wifi-router' | 'wifi-extender' | 'setup';
	network: NetworkConfig;
	ports: PortView[];
	modem: ModemStatus;
	pi_model_string?: string;
}

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

/** Live process state of a plugin with a runtime (Exec set). */
export interface PluginRuntime {
	state: 'stopped' | 'running' | 'crashed';
	pid?: number;
	restarts?: number;
	last_error?: string;
	since?: string;
}

export interface Plugin {
	id: string;
	name: string;
	version: string;
	description?: string;
	enabled: boolean;
	/** Sidebar menu items contributed by this plugin (read from manifest). */
	menu?: PluginMenuItem[];
	/** argv the runtime launches; absent for metadata-only plugins. */
	exec?: string[];
	/** Host-API scopes the plugin declares. */
	permissions?: string[];
	/** Live process state; absent when the plugin has no runtime. */
	runtime?: PluginRuntime;
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
	/** Manually paused (internet blocked) right now. */
	paused?: boolean;
	/** When the pause auto-resumes (far future = until manually resumed). */
	pause_until?: string;
	/** Approved under quarantine mode. */
	approved?: boolean;
}

export interface DevicesResponse {
	devices: Device[];
}

export interface AccessSettings {
	quarantine_new_devices: boolean;
	block_landing_page: boolean;
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
	/** When non-empty (with route_via set), only these domain suffixes tunnel — split routing. */
	route_domains?: string[];
	/** Force search engines / YouTube into safe mode for devices on this profile. */
	safe_search?: boolean;
	builtin: boolean;
}

export interface ProfilesResponse {
	profiles: Profile[];
}

// --- Bandwidth (M32) ------------------------------------------------------

export interface Sample {
	at: string;
	kbps_in: number;
	kbps_out: number;
}

export interface BandwidthStats {
	mac: string;
	last_sample: Sample;
	sparkline: Sample[];
	cum_in: number;
	cum_out: number;
}

export interface BandwidthResponse {
	devices: BandwidthStats[];
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
	app_configured?: boolean;
	app_id?: number;
	proxy_enabled?: boolean;
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
	status: 'direct' | 'tunnel' | 'kill' | 'split';
	profile_id: string;
}

export interface RoutingResponse {
	devices: RoutingDevice[];
	missing_outbounds: string[];
}

// --- Port forwarding ------------------------------------------------------

export interface PortForward {
	id: string;
	description?: string;
	proto: 'tcp' | 'udp' | 'tcp/udp';
	wan_port: number;
	dest_ip: string;
	dest_port?: number;
	enabled: boolean;
}

export interface PortForwardsResponse {
	port_forwards: PortForward[];
	/** False when the router has no WAN of its own (extender role) — forwards won't take effect. */
	router_mode: boolean;
}

// --- Zapret (DPI bypass) --------------------------------------------------

export interface ZapretPreset {
	id: string;
	name: string;
	desc: string;
}

export interface ZapretResponse {
	enabled: boolean;
	strategy: string;
	custom_args: string;
	presets: ZapretPreset[];
	autotune?: {
		results: ZapretTuneResult[];
		winner: string; // "" when no strategy worked
		at: string;
	} | null;
	status: {
		running: boolean;
		binary_present: boolean;
		router_mode: boolean;
	};
}

export interface ZapretTuneResult {
	strategy: string;
	name: string;
	ok: number;
	total: number;
	latency_ms: number;
}

export interface ZapretAutoTuneResponse {
	winner: string; // "" when no strategy worked
	results: ZapretTuneResult[];
}

// --- Cellular modem (WAN) -------------------------------------------------

export interface ModemStatus {
	present: boolean;
	state?: string;
	operator?: string;
	tech?: string;
	signal_percent: number;
	interface?: string;
	manufacturer?: string;
	model?: string;
	lock_required?: string;
	last_error?: string;
	sim_slots?: number;
	primary_slot?: number;
}

export interface ModemSignalSample {
	at: string;
	percent: number;
}

export interface ModemUsage {
	cycle_start: string;
	rx_bytes: number;
	tx_bytes: number;
	total_bytes: number;
	signal: ModemSignalSample[];
}

export interface ModemSMS {
	id: string;
	number: string;
	text: string;
	timestamp?: string;
	sent: boolean;
}

export interface ModemNetwork {
	supported_modes: string[];
	current_modes: string[];
	supported_bands: string[];
	current_bands: string[];
}

export interface ModemResponse {
	as_wan: boolean;
	apn: string;
	username: string;
	has_pin: boolean;
	sim_slot: number;
	data_limit_mb: number;
	cycle_reset_day: number;
	usage?: ModemUsage;
	status: ModemStatus;
	router_mode: boolean;
}
