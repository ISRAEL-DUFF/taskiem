// Sending a place in a chat with a person (docs/spec/people-chatting.md §7;
// the server's As built, "Places": `location {lat, lng, label?}` on send).
// The phone is asked where it is only when the person taps A place, once:
// nothing follows them. Before it goes they see it (where, how close, on
// the map if they want), and may give it a name. When the phone says no,
// or its location is off, plain words say so and how to change it. A place
// someone sent opens in the phone's own map app.
//
// The phone's location is behind [PlaceFinder], so a test gives its own.

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:geolocator/geolocator.dart' as geo;
import 'package:url_launcher/url_launcher.dart';

import '../api/api.g.dart';
import '../app/lock.dart' show stepOut;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkFormGroup;
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;

/// Why the place could not be read.
enum PlaceProblem {
  /// The phone's location is off.
  off,

  /// The person said no this time.
  refused,

  /// Said no for good: only the phone's settings can change it.
  refusedForever,

  /// Read but not found in time, or the phone failed.
  failed,
}

/// The phone's location, behind one face (a test gives its own).
abstract class PlaceFinder {
  const PlaceFinder();

  /// Asks the phone (its own question, only now) and reads where it is
  /// once; throws [PlaceRefused] when it cannot.
  Future<({double lat, double lng, double? accuracy})> here();

  /// Opens the phone's settings where [problem] can be changed.
  Future<void> openSettings(PlaceProblem problem);
}

class PlaceRefused implements Exception {
  const PlaceRefused(this.problem);
  final PlaceProblem problem;
}

/// The phone's own location, through geolocator: asked for when in use,
/// read once.
class DevicePlaceFinder extends PlaceFinder {
  const DevicePlaceFinder();

  @override
  Future<({double lat, double lng, double? accuracy})> here() async {
    if (!await geo.Geolocator.isLocationServiceEnabled()) throw const PlaceRefused(PlaceProblem.off);
    var p = await geo.Geolocator.checkPermission();
    if (p == geo.LocationPermission.denied) p = await stepOut(geo.Geolocator.requestPermission);
    if (p == geo.LocationPermission.deniedForever) throw const PlaceRefused(PlaceProblem.refusedForever);
    if (p == geo.LocationPermission.denied || p == geo.LocationPermission.unableToDetermine) throw const PlaceRefused(PlaceProblem.refused);
    try {
      final at = await geo.Geolocator.getCurrentPosition(locationSettings: const geo.LocationSettings(accuracy: geo.LocationAccuracy.high, timeLimit: Duration(seconds: 20)));
      return (lat: at.latitude, lng: at.longitude, accuracy: at.accuracy > 0 ? at.accuracy : null);
    } on geo.PermissionDeniedException {
      throw const PlaceRefused(PlaceProblem.refused);
    } on geo.LocationServiceDisabledException {
      throw const PlaceRefused(PlaceProblem.off);
    } on Object {
      throw const PlaceRefused(PlaceProblem.failed);
    }
  }

  @override
  Future<void> openSettings(PlaceProblem problem) async {
    try {
      await stepOut(problem == PlaceProblem.off ? geo.Geolocator.openLocationSettings : geo.Geolocator.openAppSettings);
    } on Object {
      // Nothing opened: the words say where to go.
    }
  }
}

/// The plain words for a place that could not be read.
String placeProblemWords(PlaceProblem p) => switch (p) {
  PlaceProblem.off => 'Your phone’s location is off. Turn it on in your phone’s settings to send where you are.',
  PlaceProblem.refused => 'Taskiem wasn’t allowed to see where you are, so nothing was sent. Tap Try again and choose Allow.',
  PlaceProblem.refusedForever => 'Taskiem isn’t allowed to see where you are. To send a place, allow Location for Taskiem in your phone’s settings.',
  PlaceProblem.failed => 'Your phone couldn’t find where you are just now. Try again, outside or near a window if you can.',
};

/// A place in the phone's own map app: on Android a `geo:` link, which any
/// map app takes; on an iPhone Apple Maps' link.
Uri mapUriOf(PeopleLocation l, {TargetPlatform? platform}) {
  final at = '${l.lat},${l.lng}';
  final label = (l.label ?? '').trim();
  if ((platform ?? defaultTargetPlatform) == TargetPlatform.iOS) {
    return Uri.https('maps.apple.com', '/', {'ll': at, 'q': label.isEmpty ? at : label});
  }
  return Uri.parse('geo:$at?q=${Uri.encodeComponent(label.isEmpty ? at : '$at($label)')}');
}

/// The same place on the web's map, when no map app takes the link.
Uri webMapOf(PeopleLocation l) => Uri.https('www.google.com', '/maps/search/', {'api': '1', 'query': '${l.lat},${l.lng}'});

/// Opens [l] in the phone's map app (or the web's map); false when nothing
/// could. A test stands in for it.
Future<bool> Function(PeopleLocation l) openPlace = (l) async {
  try {
    if (await launchUrl(mapUriOf(l), mode: LaunchMode.externalApplication)) return true;
  } on Object {
    // No map app took it: the web's map.
  }
  try {
    return await launchUrl(webMapOf(l), mode: LaunchMode.externalApplication);
  } on Object {
    return false;
  }
};

/// Where a place is, in words: "6.52440, 3.37920".
String placeWords(double lat, double lng) => '${lat.toStringAsFixed(5)}, ${lng.toStringAsFixed(5)}';

/// A place to send: the phone asked now, the place shown, a name if wanted;
/// null when put away or not found.
Future<PeopleLocation?> pickPlace(BuildContext context, PlaceFinder finder) => showTkSheet<PeopleLocation>(context, (_) => TkAppLook(child: _PlaceSheet(finder: finder)));

class _PlaceSheet extends StatefulWidget {
  const _PlaceSheet({required this.finder});
  final PlaceFinder finder;

  @override
  State<_PlaceSheet> createState() => _PlaceSheetState();
}

class _PlaceSheetState extends State<_PlaceSheet> {
  ({double lat, double lng, double? accuracy})? _at;
  PlaceProblem? _problem;
  bool _busy = true;
  final _label = TextEditingController();

  @override
  void initState() {
    super.initState();
    _find();
  }

  @override
  void dispose() {
    _label.dispose();
    super.dispose();
  }

  Future<void> _find() async {
    setState(() => (_busy = true, _problem = null));
    try {
      final at = await widget.finder.here();
      if (mounted) setState(() => (_at = at, _busy = false));
    } on PlaceRefused catch (e) {
      if (mounted) setState(() => (_problem = e.problem, _busy = false));
    } on Object {
      if (mounted) setState(() => (_problem = PlaceProblem.failed, _busy = false));
    }
  }

  PeopleLocation? get _place {
    final at = _at;
    if (at == null) return null;
    final label = _label.text.trim();
    return PeopleLocation(lat: at.lat, lng: at.lng, label: label.isEmpty ? null : label);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final at = _at;
    final problem = _problem;
    return TkSheet(
      title: 'Send a place',
      footer: at == null
          ? (problem == null ? null : TkCta(label: 'Try again', onTap: _find))
          : TkCta(label: 'Send this place', onTap: () => Navigator.of(context).pop(_place)),
      body: TkFormBody(
        children: [
          if (_busy) const TkLoading(label: 'Finding where you are…'),
          if (problem != null) ...[
            // A callout of the app's, its button the phone's settings.
            Semantics(
              liveRegion: true,
              child: TkCallout(
                leading: TkSquare('b-pin', color: look.orange),
                title: 'Where you are is not known',
                sub: placeProblemWords(problem),
                subLines: 4,
                color: look.orange,
                button: problem == PlaceProblem.off || problem == PlaceProblem.refusedForever ? 'Settings' : null,
                semantics: 'Open your phone’s settings',
                onPressed: problem == PlaceProblem.off || problem == PlaceProblem.refusedForever ? () => widget.finder.openSettings(problem) : null,
              ),
            ),
          ],
          if (at != null) ...[
            Semantics(
              container: true,
              label: 'Where you are now: ${placeWords(at.lat, at.lng)}${at.accuracy == null ? '' : ', to within ${at.accuracy!.round()} metres'}',
              excludeSemantics: true,
              child: Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(color: t.tkSoft, borderRadius: BorderRadius.circular(16)),
                child: Row(
                  children: [
                    TkIcon('b-pin', size: 22, stroke: 2, color: look.redInk),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text('Where you are now', style: tkText(size: 15, weight: 600, color: t.text)),
                          Text(placeWords(at.lat, at.lng), style: tkText(size: 13.5, color: t.muted)),
                          if (at.accuracy != null) Text('To within about ${at.accuracy!.round()} m', style: tkText(size: 13, color: t.muted)),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
            Center(child: TkLink(label: 'See it on the map', bold: true, onTap: () => openPlace(_place!))),
            TkFormGroup(children: [TkKitField(label: 'A name for it', hint: 'Optional: “Our gate”, “Mama’s shop”', child: TkTextBox(key: const ValueKey('place-label'), controller: _label, max: 200, size: 15, height: 46))]),
            Text('It is sent once, as it is now. Taskiem doesn’t follow where you go.', style: tkHint(t)),
          ],
        ],
      ),
    );
  }
}
