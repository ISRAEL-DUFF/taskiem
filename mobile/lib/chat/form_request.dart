// A form step for you, answered in the app (docs/spec/device-fields.md §4,
// D1). Its location is read only when the person taps Use my location,
// after the question's why, once; its photo is taken with the camera,
// never chosen from the gallery. A refusal is an answer (Can't). Made with
// no connection, the answer is kept on the phone and sent when it is back,
// marked as made offline (online: false), so a rider in a dead spot is not
// stuck. A code is scanned with the camera (or typed where the question
// allows), and a signature drawn on the screen with a finger, sent as a PNG
// beside the photo (D2): a rider hands the phone to the customer to sign.
//
// The phone's location is behind [formPlaceFinder], its camera behind
// [takeFormPhoto] and [codeScanner], so a test gives its own.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:image_picker/image_picker.dart' show ImagePicker, ImageSource;

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError, Offline;
import '../app/auth.dart';
import '../app/lock.dart' show stepOut;
import '../kit/forms.dart' show errorWords;
import '../kit/kit.dart';
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart' show TkCallout, TkSquare;
import '../kit/mobile.dart' show TkFormGroup, TkListRow, TkSection;
import '../kit/page.dart';
import '../kit/sheet.dart' show TkCta, TkKitField, TkLines;
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'code_scanner.dart';
import 'people_place.dart' show DevicePlaceFinder, PlaceFinder, PlaceProblem, PlaceRefused, placeProblemWords;
import 'signature_pad.dart';
import 'today_tab.dart' show shortWhen;
import 'words.dart' show CardTone;

/// Takes a photo with the camera for a form step (a test stands in).
typedef FormPhotoTake = Future<({List<int> bytes, String name})?> Function();

/// The camera, not the gallery (§3): a photo taken now, cleaned on the
/// server to at most 2,000 px.
FormPhotoTake takeFormPhoto = () async {
  final x = await stepOut(() => ImagePicker().pickImage(source: ImageSource.camera, imageQuality: 85, maxWidth: 2000));
  if (x == null) return null;
  return (bytes: await x.readAsBytes(), name: 'photo.jpg');
};

/// Where the phone is, for a form step's location (a test gives its own).
PlaceFinder formPlaceFinder = const DevicePlaceFinder();

/// The device kinds a form step's field may be.
const _deviceKinds = {'location', 'photo', 'scan', 'signature'};

/// The key the answers made offline are kept under, oldest first.
const keptFormAnswersKey = 'form-answers-out';

String _label(String name, Map<String, dynamic> p) {
  final f = p['x-form'];
  final l = f is Map ? f['label'] : null;
  if (l is String && l.isNotEmpty) return l;
  if (p['title'] is String && (p['title'] as String).isNotEmpty) return p['title'] as String;
  final s = name.replaceAll('_', ' ');
  return s.isEmpty ? s : s[0].toUpperCase() + s.substring(1);
}

String _now(DateTime t) => '${t.toUtc().toIso8601String().split('.').first}Z';

/// Sends the answers made offline, oldest first, now that there is a
/// connection; stops at the first that cannot go yet. An answer the server
/// will never take (the question closed, answers refused) is dropped.
/// Answers how many went.
Future<int> sendKeptFormAnswers(Auth auth) async {
  final store = auth.local;
  if (store == null) return 0;
  final kept = store.read(keptFormAnswersKey);
  if (kept is! List || kept.isEmpty) return 0;
  final left = <Object?>[...kept];
  var sent = 0;
  while (left.isNotEmpty) {
    final k = left.first;
    if (k is! Map) {
      left.removeAt(0);
      continue;
    }
    final id = k['id'] as String? ?? '';
    final photoKey = k['photo'] as String?;
    final signKey = k['signature'] as String?;
    try {
      final files = <_Part>[];
      if (photoKey != null) {
        final photo = await store.media(photoKey);
        if (photo != null && k['field'] is String) files.add(_photoPart(k['field'] as String, photo));
      }
      if (signKey != null) {
        final png = await store.media(signKey);
        if (png != null && k['signature_field'] is String) files.add(_signaturePart(k['signature_field'] as String, png));
      }
      await _sendAnswer(auth, id, (k['answers'] as Map).cast<String, dynamic>(), (k['device'] as Map).cast<String, dynamic>(), files);
    } on Offline {
      break;
    } on ApiError {
      // Closed, or refused: it cannot go, now or later.
    }
    if (photoKey != null) store.dropMedia(photoKey);
    if (signKey != null) store.dropMedia(signKey);
    left.removeAt(0);
    sent++;
    store.write(keptFormAnswersKey, left.isEmpty ? null : left);
  }
  return sent;
}

/// A file sent with an answer, as a part named by its question.
typedef _Part = ({String field, List<int> bytes, String filename, String type});

_Part _photoPart(String field, List<int> bytes) => (field: field, bytes: bytes, filename: 'photo.jpg', type: 'image/jpeg');
_Part _signaturePart(String field, List<int> bytes) => (field: field, bytes: bytes, filename: 'signature.png', type: 'image/png');

Future<void> _sendAnswer(Auth auth, String id, Map<String, dynamic> answers, Map<String, dynamic> device, List<_Part> files) async {
  if (files.isNotEmpty) {
    await auth.transport.uploadFiles('/v1/form-requests/${Uri.encodeComponent(id)}/answers', files: files, fields: {'answers': jsonEncode(answers), 'device': jsonEncode(device)});
    return;
  }
  await auth.api.answerFormRequest(id: id, body: AnswerFormRequestRequest(answers: answers, device: FormDevice.fromJson(device)));
}

/// One form step for the person: its questions, the device ones at a tap.
class FormRequestScreen extends StatefulWidget {
  const FormRequestScreen({super.key, required this.auth, required this.id, this.now, this.backLabel = 'Back to Today'});
  final Auth auth;
  final String id;
  final DateTime Function()? now;
  final String backLabel;

  @override
  State<FormRequestScreen> createState() => _FormRequestScreenState();
}

class _FormRequestScreenState extends State<FormRequestScreen> {
  FormRequest? _req;
  String? _loadError;
  final _device = <String, Map<String, dynamic>>{};
  final _typed = <String, TextEditingController>{};
  ({List<int> bytes, String name})? _photo;
  String? _photoField;

  /// What was signed on the pad, and its question.
  Signed? _signed;
  String? _signedField;
  String? _reading;
  PlaceProblem? _placeProblem;
  bool _busy = false;
  String? _error;
  final _missing = <String>{};

  /// sent or kept, once answered.
  String? _done;

  DateTime _clock() => (widget.now ?? DateTime.now)();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    for (final c in _typed.values) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _load() async {
    setState(() => _loadError = null);
    // Anything kept from before goes first, now that we may be online.
    try {
      await sendKeptFormAnswers(widget.auth);
    } on Object {
      // Tried again next time.
    }
    try {
      final r = await widget.auth.api.getFormRequest(id: widget.id);
      if (!mounted) return;
      setState(() {
        _req = r;
        for (final e in _props(r).entries) {
          if (!_deviceKinds.contains(e.value['type'])) {
            final v = r.prefill?[e.key];
            _typed[e.key] = TextEditingController(text: v == null ? '' : '$v');
          }
        }
      });
    } on Object catch (e) {
      if (mounted) setState(() => _loadError = errorWords(e));
    }
  }

  Map<String, Map<String, dynamic>> _props(FormRequest r) {
    final p = r.schema['properties'];
    if (p is! Map) return const {};
    return {
      for (final e in p.entries)
        if (e.value is Map) e.key as String: (e.value as Map).cast<String, dynamic>(),
    };
  }

  Set<String> _required(FormRequest r) => {for (final x in (r.schema['required'] as List? ?? const [])) '$x'};

  /// The location, read now, at the tap: the phone's own question first.
  Future<void> _readPlace(String field) async {
    setState(() {
      _reading = field;
      _placeProblem = null;
    });
    try {
      final at = await formPlaceFinder.here();
      if (!mounted) return;
      setState(() {
        _device[field] = {'lat': at.lat, 'lng': at.lng, 'accuracy_m': ?at.accuracy?.round(), 'at': _now(_clock()), 'fix': 'gps'};
        _missing.remove(field);
        if (_missing.isEmpty) _error = null;
      });
    } on PlaceRefused catch (e) {
      if (!mounted) return;
      setState(() {
        _placeProblem = e.problem;
        _device[field] = e.problem == PlaceProblem.refused || e.problem == PlaceProblem.refusedForever
            ? {'state': 'refused', 'reason': 'the phone was not allowed to read where I am'}
            : {'state': 'unavailable', 'reason': 'the phone could not read where I am'};
        _missing.remove(field);
        if (_missing.isEmpty) _error = null;
      });
    } finally {
      if (mounted) setState(() => _reading = null);
    }
  }

  Future<void> _takePhoto(String field) async {
    final p = await takeFormPhoto();
    if (p == null || !mounted) return;
    setState(() {
      _photo = p;
      _photoField = field;
      _device.remove(field);
      _missing.remove(field);
      if (_missing.isEmpty) _error = null;
    });
  }

  /// The code, read by the camera at the tap (or typed where the question
  /// allows), after the question's why.
  Future<void> _scan(String field, Map<String, dynamic> p) async {
    final formats = (p['formats'] as List?)?.whereType<String>().toList();
    final got = await codeScanner.scanOne(
      context,
      title: _label(field, p),
      why: p['why'] as String?,
      formats: formats,
      allowTyped: p['allow_typed'] != false,
      backLabel: 'Back to ${_req?.title ?? 'the question'}',
    );
    if (got == null || !mounted) return;
    setState(() {
      _device[field] = got.toJson();
      _missing.remove(field);
      if (_missing.isEmpty) _error = null;
    });
  }

  /// The pad: the person (or the customer, handed the phone) signs.
  Future<void> _sign(String field, Map<String, dynamic> p) async {
    final got = await signOnScreen(context, title: _label(field, p), why: p['why'] as String?, askName: p['ask_name'] == true, backLabel: 'Back to ${_req?.title ?? 'the question'}');
    if (got == null || !mounted) return;
    setState(() {
      _signed = got;
      _signedField = field;
      _device.remove(field);
      _missing.remove(field);
      if (_missing.isEmpty) _error = null;
    });
  }

  void _cannot(String field) => setState(() {
    _device[field] = {'state': 'refused', 'reason': 'I cannot answer this here'};
    if (_photoField == field) {
      _photo = null;
      _photoField = null;
    }
    if (_signedField == field) {
      _signed = null;
      _signedField = null;
    }
    _missing.remove(field);
    if (_missing.isEmpty) _error = null;
  });

  Future<void> _send() async {
    final r = _req;
    if (r == null) return;
    final props = _props(r);
    final need = _required(r);
    final answers = <String, dynamic>{};
    _missing.clear();
    for (final e in props.entries) {
      final kind = e.value['type'];
      if (_deviceKinds.contains(kind)) {
        final signed = kind == 'signature' && _signedField == e.key && _signed != null;
        if (signed) {
          // The drawing goes as a part; the signer's name beside it.
          if (_signed!.name != null) answers[e.key] = {'signed_by': _signed!.name};
        } else if (_device.containsKey(e.key)) {
          answers[e.key] = _device[e.key];
        } else if (!(kind == 'photo' && _photoField == e.key) && need.contains(e.key)) {
          _missing.add(e.key);
        }
        continue;
      }
      final text = _typed[e.key]?.text.trim() ?? '';
      if (text.isEmpty) {
        if (need.contains(e.key)) _missing.add(e.key);
        continue;
      }
      answers[e.key] = switch (kind) {
        'integer' => int.tryParse(text) ?? text,
        'number' => num.tryParse(text) ?? text,
        _ => text,
      };
    }
    if (_missing.isNotEmpty) {
      setState(() => _error = 'Answer ${_missing.map((m) => _label(m, props[m]!)).join(' and ')}, or tap Can’t.');
      return;
    }
    final device = {'app': 'app', 'version': widget.auth.version, 'online': true, 'at': _now(_clock())};
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await _sendAnswer(widget.auth, r.id, answers, device, _files());
      if (mounted) setState(() => _done = 'sent');
    } on Offline {
      // No connection: kept, and sent when it is back, as made offline.
      final store = widget.auth.local;
      if (store == null) {
        if (mounted) setState(() => _error = 'You’re offline. Try again when you’re back online.');
      } else {
        String? photoKey;
        if (_photo != null) {
          photoKey = 'form-photo:${r.id}';
          await store.keepMedia(photoKey, _photo!.bytes);
        }
        String? signKey;
        if (_signed != null) {
          signKey = 'form-signature:${r.id}';
          await store.keepMedia(signKey, _signed!.png);
        }
        final kept = store.read(keptFormAnswersKey);
        store.write(keptFormAnswersKey, [
          if (kept is List) ...kept,
          {
            'id': r.id,
            'answers': answers,
            'device': {...device, 'online': false},
            'field': _photoField,
            'photo': photoKey,
            'signature_field': _signedField,
            'signature': signKey,
          },
        ]);
        if (mounted) setState(() => _done = 'kept');
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  /// The files this answer sends: the photo and the signature.
  List<_Part> _files() => [
    if (_photoField != null && _photo != null) _photoPart(_photoField!, _photo!.bytes),
    if (_signedField != null && _signed != null) _signaturePart(_signedField!, _signed!.png),
  ];

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final r = _req;
    if (r == null) {
      return TkPageScreen(
        title: 'To fill in',
        titleSize: 22,
        backLabel: widget.backLabel,
        children: [
          if (_loadError == null) const TkLoading(rows: 3, label: 'Loading the question…'),
          if (_loadError != null)
            TkCallout(leading: TkSquare('b-alert', color: look.orange), title: 'This could not be opened', sub: _loadError, color: look.orange, button: 'Try again', onPressed: _load),
        ],
      );
    }
    final who = r.from ?? ((r.role ?? '').isNotEmpty ? 'Your ${r.role} role' : 'Your business');
    if (_done != null) {
      final kept = _done == 'kept';
      return TkPageScreen(
        title: r.title,
        titleSize: 22,
        backLabel: widget.backLabel,
        children: [
          TkCallout(
            leading: TkSquare(kept ? 'b-clock' : 'b-check', color: kept ? look.orange : look.teal),
            title: kept ? 'Kept on your phone' : 'Sent',
            sub: kept ? 'You’re offline. It goes to $who as soon as you’re back online.' : '$who has your answer. The work carries on.',
            subLines: 3,
            color: kept ? look.orange : look.teal,
          ),
          TkCta(label: widget.backLabel, onTap: () => Navigator.of(context).maybePop()),
        ],
      );
    }
    final props = _props(r);
    final need = _required(r);
    final now = _clock();
    final typed = [for (final e in props.entries) if (!_deviceKinds.contains(e.value['type'])) e];
    return TkPageScreen(
      title: r.title,
      titleSize: 22,
      backLabel: widget.backLabel,
      description: r.description is String ? r.description as String : null,
      children: [
        TkFormGroup(children: [
          TkLines([('Asked by', who), ('Open until', shortWhen(r.expiresAt.toIso8601String(), now))]),
        ]),
        for (final e in props.entries)
          if (_deviceKinds.contains(e.value['type'])) _deviceRow(context, e.key, e.value, need.contains(e.key), look),
        if (typed.isNotEmpty)
          TkFormGroup(children: [
            for (final e in typed)
              TkKitField(
                label: '${_label(e.key, e.value)}${need.contains(e.key) ? '' : ' (optional)'}',
                error: _missing.contains(e.key) ? 'Answer this' : null,
                child: TkTextBox(controller: _typed[e.key]!, size: 15, height: 46),
              ),
          ]),
        if (_placeProblem != null)
          TkCallout(
            leading: TkSquare('b-pin', color: look.orange),
            title: 'Your location was not read',
            sub: placeProblemWords(_placeProblem!),
            subLines: 3,
            color: look.orange,
            button: _placeProblem == PlaceProblem.refusedForever || _placeProblem == PlaceProblem.off ? 'Settings' : null,
            onPressed: () => formPlaceFinder.openSettings(_placeProblem!),
          ),
        if (_error != null) TkCallout(leading: TkSquare('b-x', color: look.red), title: 'Not sent', sub: _error, subLines: 3, color: look.red),
        TkCta(label: _busy ? 'Sending…' : 'Send', onTap: _busy ? null : _send),
      ],
    );
  }

  Widget _deviceRow(BuildContext context, String name, Map<String, dynamic> p, bool required, TkLook look) {
    final kind = p['type'] as String;
    final why = p['why'] is String ? 'Why: ${p['why']}' : '';
    final label = '${_label(name, p)}${required ? '' : ' (optional)'}';
    final v = _device[name];
    final refused = v != null && v['state'] != null;
    String? status;
    var tone = CardTone.lime;
    var sub = why;
    Widget? leading;
    String title;
    VoidCallback? tap;
    String icon;
    Color square;
    switch (kind) {
      case 'location':
        icon = 'b-pin';
        square = look.teal;
        title = _reading == name ? 'Reading your location…' : (v != null && !refused ? 'Read it again' : 'Use my location');
        tap = _reading == null ? () => _readPlace(name) : null;
        if (v != null && !refused) {
          status = 'Read';
          sub = v['accuracy_m'] != null ? 'Read by your phone, to within ${v['accuracy_m']} m' : 'Read by your phone';
        }
      case 'photo':
        icon = 'b-camera';
        square = look.violet;
        title = _photoField == name ? 'Take it again' : 'Take a photo';
        tap = () => _takePhoto(name);
        if (_photoField == name && _photo != null) {
          status = 'Taken';
          sub = 'Taken with your camera now';
          leading = ClipRRect(
            borderRadius: BorderRadius.circular(10),
            child: Image.memory(Uint8List.fromList(_photo!.bytes), width: 44, height: 44, fit: BoxFit.cover, semanticLabel: 'The photo you took', errorBuilder: (_, _, _) => const SizedBox(width: 44, height: 44)),
          );
        }
      case 'scan':
        icon = 'b-qr';
        square = look.blue;
        final code = v != null && !refused ? v : null;
        title = code != null ? 'Scan it again' : 'Scan the code';
        tap = () => _scan(name, p);
        if (code != null) {
          status = code['format'] == null ? 'Typed' : 'Scanned';
          sub = '${code['text']} · ${formatWords(code['format'] as String?)}';
        }
      default:
        icon = 'b-note';
        square = look.teal;
        final signed = _signedField == name && _signed != null;
        title = signed ? 'Sign again' : 'Sign on the screen';
        tap = () => _sign(name, p);
        if (signed) {
          status = 'Signed';
          sub = _signed!.name != null ? 'Signed by ${_signed!.name}' : 'Signed on this phone';
          leading = Container(
            width: 44,
            height: 44,
            decoration: BoxDecoration(borderRadius: BorderRadius.circular(10), color: const Color(0xFFFFFFFF)),
            clipBehavior: Clip.antiAlias,
            child: Image.memory(_signed!.png, fit: BoxFit.contain, semanticLabel: 'The signature', errorBuilder: (_, _, _) => const SizedBox(width: 44, height: 44)),
          );
        }
    }
    if (refused) {
      status = 'Can’t';
      tone = CardTone.warn;
      sub = 'You said you can’t answer this';
    }
    if (_missing.contains(name)) {
      status = 'Needed';
      tone = CardTone.danger;
    }
    final answered = status != null && status != 'Needed';
    return TkSection(
      title: label,
      inset: 56,
      children: [
        TkListRow(
          title: title,
          sub: sub,
          subLines: 3,
          icon: leading == null ? icon : null,
          leading: leading,
          square: square,
          status: status,
          statusTone: tone,
          chevron: false,
          onTap: tap,
          semanticsLabel: '$title: $label',
          trailing: !answered
              ? Padding(
                  padding: const EdgeInsets.only(right: 4),
                  child: TkSmallButton(label: 'Can’t', semantics: 'I can’t answer $label', onPressed: () => _cannot(name)),
                )
              : null,
        ),
      ],
    );
  }
}
