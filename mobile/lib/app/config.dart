// Where the app finds Taskiem: given when the app is built
// (`--dart-define=TASKIEM_URL=https://...`). Plain http only to this
// machine or the emulator's host, for development (lib/api/transport.dart).

const taskiemUrl = String.fromEnvironment('TASKIEM_URL', defaultValue: 'https://staging.ispend.name.ng');

/// The app's version, which the server's lowest served version is checked
/// against (GET /v1/app/config).
const appVersion = String.fromEnvironment('TASKIEM_APP_VERSION', defaultValue: '0.1.0');
