// Your tools (made tools M1; docs/tools.md, docs/spec/made-tools.md §6–§8):
// a business's own workflows that its people call from the Taskiem
// conversation. Their tiles follow the built-in ones in the tools panel and
// "/" finds them by command or name; a tile opens the tool's sheet, its
// form drawn from the tool's inputs schema; then its care (answers at once
// with an echo of every value, asks Yes, or asks Yes and a passkey); then
// its answer in the conversation, read from the run when it ends (a text,
// a card, a list).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart' show openInBrowser;
import '../app/passkey.dart';
import '../kit/forms.dart' show TkSwitch, errorWords, nairaKobo;
import '../kit/icon.dart' show TkIcon;
import '../kit/page.dart' show TkPageScreen;
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../theme/icons.g.dart';
import '../theme/look.dart';
import '../theme/look_icons.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart';
import 'code_scanner.dart';
import 'form_request.dart' show formPlaceFinder, takeFormPhoto;
import 'people_place.dart' show PlaceProblem, PlaceRefused, placeProblemWords;
import 'signature_pad.dart' show Signed, signOnScreen;
import 'groups.dart' show pickAction, pickOneOf;
import 'saved_fill.dart';
import 'words.dart';

// --- the tools ---

/// A made tool's key among the panel's tiles and Recent ("tool:expense"),
/// apart from the built-in tools' keys.
String madeKey(MadeTool t) => 'tool:${t.command}';

/// The tool's icon: its own when the app draws it, else a bolt.
String madeIcon(MadeTool t) {
  final i = t.icon ?? '';
  for (final n in [i, 'b-$i']) {
    if (n.isNotEmpty && (tkIconPaths.containsKey(n) || tkLookIcons.containsKey(n))) return n;
  }
  return 'b-bolt';
}

/// The tools "/" and what follows find: the command's start, or a word of
/// the name's.
List<MadeTool> madeMatching(List<MadeTool> tools, String query) {
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return tools;
  return [
    for (final t in tools)
      if (t.command.startsWith(q) || t.name.toLowerCase().split(RegExp(r'\s+')).any((w) => w.startsWith(q))) t,
  ];
}

/// The tool "/command words" names and the words after it (made tools §6,
/// "with words after it"), so its form opens filled in from them; null for
/// "/command" alone (the panel opens it) and for no tool of the person's.
({MadeTool tool, String words})? madeByCommand(List<MadeTool> tools, String text) {
  final m = RegExp(r'^/([a-z][a-z0-9-]*)\s+(\S[\s\S]*)$').firstMatch(text.trim());
  if (m == null) return null;
  final tool = tools.where((t) => t.command == m.group(1)).firstOrNull;
  if (tool == null) return null;
  // The reader takes at most 300 characters (POST …/read): more is cut
  // rather than refused, so the form still opens filled in.
  final words = m.group(2)!.replaceAll(RegExp(r'\s+'), ' ').trim();
  return (tool: tool, words: String.fromCharCodes(words.runes.take(300)));
}

/// The tool a sentence starts with a phrase of (web/src/lib/tools.ts
/// madeToolWords, "phrase"): "i bought fuel for 4500" names the tool that
/// says "i bought", and the words after the phrase are read into its form.
/// The longest phrase wins; null when no phrase starts it, or when the
/// longest is said by two tools (the chat asks which), and for a "/command".
({MadeTool tool, String words})? madeByPhrase(List<MadeTool> tools, String text) {
  final t = text.trim().replaceAll(RegExp(r'\s+'), ' ');
  if (t.isEmpty || t.startsWith('/')) return null;
  final folded = t.toLowerCase();
  MadeTool? best;
  var len = 0;
  var tie = false;
  for (final tool in tools) {
    for (final say in tool.says) {
      final p = say.trim().toLowerCase().replaceAll(RegExp(r'\s+'), ' ');
      if (p.isEmpty || !(folded == p || folded.startsWith('$p '))) continue;
      if (best != null && len == p.length && best.command != tool.command) {
        tie = true;
      } else if (best == null || p.length > len) {
        best = tool;
        len = p.length;
        tie = false;
      }
    }
  }
  if (best == null || tie) return null;
  return (tool: best, words: String.fromCharCodes(t.substring(len).trim().runes.take(300)));
}

/// "Open X, filled in" over the Taskiem conversation's box, when what is
/// typed starts with a phrase one of the person's tools says (the web's
/// made-tool chip): pressed, the tool's sheet opens with the words read;
/// sending the words still goes to the chat.
class MadeToolChip extends StatelessWidget {
  const MadeToolChip({super.key, required this.tool, required this.words, required this.onOpen});
  final MadeTool tool;
  final String words;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final kicker = 'Open ${tool.name}, filled in';
    final shown = words.isEmpty ? '/${tool.command}' : words;
    return Padding(
      padding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
      child: Align(
        alignment: Alignment.centerLeft,
        child: Semantics(
          container: true,
          button: true,
          label: '$kicker: $shown',
          excludeSemantics: true,
          onTap: onOpen,
          child: TkPress(
            onTap: onOpen,
            child: Container(
              constraints: const BoxConstraints(minHeight: 56, maxWidth: 520),
              padding: const EdgeInsets.fromLTRB(10, 9, 14, 9),
              decoration: BoxDecoration(color: t.panel, borderRadius: BorderRadius.circular(18), boxShadow: t.tkLift),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Container(
                    width: 38,
                    height: 38,
                    decoration: BoxDecoration(color: t.brand, shape: BoxShape.circle),
                    alignment: Alignment.center,
                    child: TkIcon(madeIcon(tool), size: 18, stroke: 2, color: t.brandDark),
                  ),
                  const SizedBox(width: 12),
                  Flexible(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(kicker.toUpperCase(), maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 11, weight: 700, color: t.muted).copyWith(letterSpacing: 0.9)),
                        Text(shown, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 600, lineHeight: 1.35, color: t.text)),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Whether the tool asks before it runs: its steps say so, or an owner
/// made it always ask.
bool madeAsks(MadeTool t) => t.alwaysAsk || t.care.level != 'answers';

bool isOwner(Auth auth) => (auth.me?.roles as List?)?.contains('owner') ?? false;

// --- the form, from the inputs schema ---

/// One field of a tool's form, as its schema property says (the web's
/// lib/forms.ts fieldTypeOf, in the app's few kinds).
class SchemaField {
  const SchemaField({
    required this.name,
    required this.label,
    required this.kind,
    required this.required,
    this.hint,
    this.choices = const [],
    this.prop = const {},
  });
  final String name;
  final String label;

  /// text, long, number, money, yes_no, choice, choices, scan (a code
  /// the phone reads, or typed: device fields D2), device, other.
  final String kind;
  final bool required;
  final String? hint;
  final List<Object?> choices;
  final Map<String, dynamic> prop;
}

const _deviceKinds = {'location', 'photo', 'scan', 'signature'};

/// The fields of an inputs schema, in the form's order (x-form.order, then
/// as written); hidden ones left out.
List<SchemaField> schemaFields(Map<String, dynamic>? schema) {
  final props = (schema?['properties'] as Map?)?.cast<String, dynamic>() ?? const {};
  final required = ((schema?['required'] as List?) ?? const []).whereType<String>().toSet();
  final out = <SchemaField>[];
  for (final e in props.entries) {
    final p = (e.value as Map?)?.cast<String, dynamic>() ?? const <String, dynamic>{};
    final x = (p['x-form'] as Map?)?.cast<String, dynamic>() ?? const <String, dynamic>{};
    if (x['hidden'] == true) continue;
    final type = p['type'];
    final items = (p['items'] as Map?)?.cast<String, dynamic>();
    final kind = type == 'scan'
        ? 'scan'
        : _deviceKinds.contains(type)
        ? 'device'
        : type == 'boolean'
        ? 'yes_no'
        : type == 'array' && items?['enum'] is List
        ? 'choices'
        : p['enum'] is List
        ? 'choice'
        : type == 'integer' || type == 'number'
        ? (x['unit'] == 'naira' ? 'money' : 'number')
        : type == 'object' || type == 'array'
        ? 'other'
        : x['widget'] == 'textarea'
        ? 'long'
        : 'text';
    final label = (x['label'] ?? p['title']) as String?;
    final words = e.key.replaceAll('_', ' ');
    out.add(
      SchemaField(
        name: e.key,
        label: label != null && label.isNotEmpty ? label : (words.isEmpty ? e.key : words[0].toUpperCase() + words.substring(1)),
        kind: kind,
        required: required.contains(e.key),
        hint: (x['help'] ?? p['description']) as String?,
        choices: kind == 'choices' ? (items!['enum'] as List) : (p['enum'] as List?) ?? const [],
        prop: p,
      ),
    );
  }
  int order(SchemaField f) => ((f.prop['x-form'] as Map?)?['order'] as num?)?.toInt() ?? 1 << 20;
  final at = {for (final (i, f) in out.indexed) f.name: i};
  out.sort((a, b) => order(a) != order(b) ? order(a) - order(b) : at[a.name]! - at[b.name]!);
  return out;
}

// --- saved things as a tool's inputs ---

/// What a Saved button can fill in a tool's form (made tools M1: "a tool's
/// input of a type a saved kind has … offers the person's and the
/// business's Saved objects of that kind"): its x-saved hint when it has
/// one, else its format, else the words of its name and label. Only a text
/// field is offered one (as web/src/lib/toolsaved.ts).
const toolSavedKinds = {'person', 'place', 'phone', 'email', 'bank_account', 'date'};

const _savedFormats = {'phone': 'phone', 'phone-ng': 'phone', 'tel': 'phone', 'email': 'email', 'date': 'date'};

final _savedWords = <(RegExp, String)>[
  (RegExp(r'\b(phone|tel|telephone|mobile|whatsapp)\b'), 'phone'),
  (RegExp(r'\b(e ?mail)\b'), 'email'),
  (RegExp(r'\b(account number|bank account|account no|nuban)\b'), 'bank_account'),
  (RegExp(r'\b(address|location|deliver to|delivery address|branch|place|site)\b'), 'place'),
  (RegExp(r'\b(date|day|due|birthday|deadline)\b'), 'date'),
  (RegExp(r'\b(customer|person|contact|recipient|client|supplier)\b'), 'person'),
];

/// The saved kind a field of a tool's form takes, or null.
String? toolSavedKind(SchemaField f) {
  if (f.kind != 'text' && f.kind != 'long') return null;
  final hint = f.prop['x-saved'];
  if (hint is String) return toolSavedKinds.contains(hint) ? hint : null;
  final format = f.prop['format'];
  if (format is String && format.isNotEmpty) return _savedFormats[format];
  final words = '${f.name} ${f.prop['title'] ?? f.label}'
      .replaceAllMapped(RegExp('([a-z])([A-Z])'), (m) => '${m[1]} ${m[2]}')
      .replaceAll(RegExp('[_-]+'), ' ')
      .toLowerCase();
  for (final (re, kind) in _savedWords) {
    if (re.hasMatch(words)) return kind;
  }
  return null;
}

/// What the Saved button of a field of [kind] says to a screen reader.
String _savedSemantics(String kind, String label) => switch (kind) {
  'person' => 'Pick a saved person for $label',
  'place' => 'Pick a saved place for $label',
  'phone' => 'Pick a saved phone number for $label',
  'email' => 'Pick a saved email for $label',
  'bank_account' => 'Fill $label from a saved bank account',
  _ => 'Pick a saved date for $label',
};

/// A saved thing of [kind] picked, as the words that fill a field: a
/// person's name, a place's address (fitted to [max]), a phone or an email
/// in full, an account's number once a passkey shows it, a date as
/// 2027-03-03. Null when none is picked or nothing can fill it.
Future<String?> pickSavedFor(BuildContext context, Auth auth, String kind, {int max = 300}) async {
  switch (kind) {
    case 'person':
      return (await pickSaved(context, auth, kind: 'person', title: 'Saved people'))?.title;
    case 'place':
      return (await pickSavedPlace(context, auth, max: max))?.address;
    case 'phone':
      return pickSavedPhone(context, auth);
    case 'email':
      bool shown(SavedObject x) {
        final e = (x.details.email ?? '').trim();
        return e.isNotEmpty && !e.contains('•');
      }
      return (await pickSaved(context, auth, kind: 'person', title: 'Saved emails', fits: shown))?.details.email?.trim();
    case 'bank_account':
      final a = await pickSavedAccount(context, auth);
      return a == null || a.number.isEmpty ? null : a.number;
    case 'date':
      return pickSavedDate(context, auth);
  }
  return null;
}

/// A value as a box shows it.
String _shown(Object? v) => switch (v) {
  null => '',
  num n when n == n.roundToDouble() => '${n.toInt()}',
  _ => '$v',
};

/// The form's values, kept while the sheet is open: typed ones in boxes,
/// chosen ones as they are.
class _FormValues {
  _FormValues(this.fields, Map<String, dynamic> initial) {
    for (final f in fields) {
      final v = initial[f.name];
      switch (f.kind) {
        case 'yes_no':
          picked[f.name] = v == true;
        case 'choice':
          if (v != null) picked[f.name] = v;
        case 'choices':
          picked[f.name] = v is List ? [...v] : <Object?>[];
        case 'text' || 'long' || 'number' || 'money':
          boxes[f.name] = TextEditingController(text: _shown(v));
        case 'scan':
          // A code read from the home screen comes as {text, format}.
          final text = v is Map ? '${v['text'] ?? ''}' : _shown(v);
          boxes[f.name] = TextEditingController(text: text);
          if (v is Map && v['format'] is String) scanned[f.name] = ScannedCode(text, v['format'] as String);
      }
    }
  }

  /// A scan field's code as the camera read it (its format); typed over,
  /// the code is the words typed.
  final scanned = <String, ScannedCode>{};

  /// A tool's location, photo and signature (build-plan-gaps H3): the
  /// location read at the tap (or a refusal), the photo taken with the
  /// camera, the signature drawn on the pad.
  final read = <String, Map<String, dynamic>>{};
  final photos = <String, ({List<int> bytes, String name})>{};
  final signed = <String, Signed>{};

  /// Whether a device field holds an answer, a refusal included.
  bool answered(SchemaField f) => read.containsKey(f.name) || photos.containsKey(f.name) || signed.containsKey(f.name);

  /// Can't: a refusal is an answer (device fields §4).
  void cannot(SchemaField f) {
    photos.remove(f.name);
    signed.remove(f.name);
    read[f.name] = {'state': 'refused', 'reason': 'I cannot answer this here'};
  }

  final List<SchemaField> fields;
  final boxes = <String, TextEditingController>{};
  final picked = <String, Object?>{};

  /// Values read from words (POST /v1/tools/{command}/read) into the
  /// fields they name; those that fit no field left out. How many filled.
  int fill(Map<String, dynamic> values) {
    var n = 0;
    for (final f in fields) {
      if (!values.containsKey(f.name)) continue;
      final v = values[f.name];
      switch (f.kind) {
        case 'yes_no':
          if (v is bool) {
            picked[f.name] = v;
            n++;
          }
        case 'choice':
          final c = f.choices.where((c) => '$c' == '$v').firstOrNull;
          if (c != null) {
            picked[f.name] = c;
            n++;
          }
        case 'choices':
          if (v is List) {
            picked[f.name] = [for (final c in f.choices) if (v.any((x) => '$x' == '$c')) c];
            n++;
          }
        case 'text' || 'long' || 'number' || 'money' || 'scan':
          boxes[f.name]!.text = v is Map ? '${v['text'] ?? ''}' : _shown(v);
          n++;
      }
    }
    return n;
  }

  void dispose() {
    for (final b in boxes.values) {
      b.dispose();
    }
  }

  /// What is wrong with a field's value, in a few words; null when fine.
  String? problem(SchemaField f) {
    final box = boxes[f.name];
    if (box == null) return null;
    final s = box.text.trim();
    if (s.isEmpty) return null;
    if (f.kind == 'money' && nairaKobo(s) == null) return 'An amount in naira, like 4,500';
    if (f.kind == 'number') {
      final n = num.tryParse(s.replaceAll(',', ''));
      if (n == null) return 'A number';
      if (f.prop['type'] == 'integer' && n != n.roundToDouble()) return 'A whole number';
    }
    return null;
  }

  bool get ready => fields.every((f) {
    if (problem(f) != null) return false;
    if (!f.required) return true;
    return switch (f.kind) {
      'yes_no' || 'other' => true,
      'device' => answered(f),
      'choice' => picked[f.name] != null,
      'choices' => (picked[f.name] as List).isNotEmpty,
      _ => boxes[f.name]!.text.trim().isNotEmpty,
    };
  });

  /// The input the start sends: what was filled in, nothing for what was
  /// not; a device field as unavailable (the app asks it in a later part).
  Map<String, dynamic> input() {
    final out = <String, dynamic>{};
    for (final f in fields) {
      switch (f.kind) {
        case 'yes_no':
          out[f.name] = picked[f.name] == true;
        case 'choice':
          if (picked[f.name] != null) out[f.name] = picked[f.name];
        case 'choices':
          if ((picked[f.name] as List).isNotEmpty) out[f.name] = picked[f.name];
        case 'device':
          // A photo or a signature goes first, on its own (the sheet's
          // start); a location as read, or a refusal.
          if (read[f.name] case final v?) out[f.name] = v;
        case 'money':
          final k = nairaKobo(boxes[f.name]!.text.trim());
          if (k != null) out[f.name] = k % 100 == 0 ? k ~/ 100 : k / 100;
        case 'number':
          final n = num.tryParse(boxes[f.name]!.text.trim().replaceAll(',', ''));
          if (n != null) out[f.name] = f.prop['type'] == 'integer' ? n.toInt() : n;
        case 'text' || 'long':
          final s = boxes[f.name]!.text.trim();
          if (s.isNotEmpty) out[f.name] = s;
        case 'scan':
          final s = boxes[f.name]!.text.trim();
          final read = scanned[f.name];
          if (s.isNotEmpty) out[f.name] = read != null && read.text.trim() == s ? read.toJson() : s;
      }
    }
    return out;
  }
}

/// The tool's form: a field for each input, in a form group.
class _SchemaForm extends StatelessWidget {
  const _SchemaForm({required this.values, required this.onChanged, this.onSaved, this.onScan, this.onDevice, this.picking, this.busy = false});

  /// A location, photo or signature input pressed: the phone is asked
  /// now, after its why (build-plan-gaps H3).
  final void Function(SchemaField f)? onDevice;

  /// A scan field's Scan pressed: the camera reads its code.
  final void Function(SchemaField f)? onScan;
  final _FormValues values;
  final VoidCallback onChanged;

  /// A field's Saved button pressed: the sheet picks and fills it.
  final void Function(SchemaField f, String kind)? onSaved;

  /// The field whose saved things are being read and picked from.
  final String? picking;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    final fields = values.fields.where((f) => f.kind != 'other' && (f.kind != 'device' || onDevice != null)).toList();
    return TkFormGroup(children: [for (final f in fields) _field(context, f)]);
  }

  String _label(SchemaField f) => f.required || f.kind == 'yes_no' ? f.label : '${f.label} (optional)';

  /// A location, a photo or a signature (build-plan-gaps H3), asked as a
  /// form step's are: its why until it is answered, then what it holds;
  /// the phone asked only at the tap; Can't is an answer.
  Widget _device(SchemaField f) {
    final kind = f.prop['type'] as String? ?? '';
    final got = values.read[f.name];
    final cant = got?['state'] != null;
    final photo = values.photos[f.name];
    final sign = values.signed[f.name];
    final why = f.prop['why'] is String ? 'Why: ${f.prop['why']}' : f.hint;
    final (String action, String icon, String? said) = switch (kind) {
      'location' => (
        got != null && !cant ? 'Read it again' : 'Use my location',
        'b-pin',
        got != null && !cant ? (got['accuracy_m'] != null ? 'Read by your phone, to within ${got['accuracy_m']} m' : 'Read by your phone') : null,
      ),
      'photo' => (photo != null ? 'Take it again' : 'Take a photo', 'b-camera', photo != null ? 'Taken with your camera now' : null),
      _ => (
        sign != null ? 'Sign again' : 'Sign on the screen',
        'b-note',
        sign != null ? (sign.name != null ? 'Signed by ${sign.name}' : 'Signed on this phone') : null,
      ),
    };
    return TkKitField(
      label: _label(f),
      hint: cant ? 'You said you can’t answer this' : (said ?? why),
      field: false,
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          TkSmallButton(label: action, icon: icon, primary: !values.answered(f), semantics: '$action: ${f.label}', onPressed: busy || onDevice == null ? null : () => onDevice!(f)),
          if (!values.answered(f))
            TkSmallButton(
              label: 'Can’t',
              semantics: 'I can’t answer ${f.label}',
              onPressed: busy
                  ? null
                  : () {
                      values.cannot(f);
                      onChanged();
                    },
            ),
        ],
      ),
    );
  }

  Widget _field(BuildContext context, SchemaField f) {
    final t = TkTheme.tokens(context);
    switch (f.kind) {
      case 'yes_no':
        return Row(
          children: [
            Expanded(
              child: Text(f.label, style: tkText(size: 15, weight: 600, color: t.text)),
            ),
            TkSwitch(
              label: f.label,
              on: values.picked[f.name] == true,
              onChanged: (v) {
                values.picked[f.name] = v;
                onChanged();
              },
            ),
          ],
        );
      case 'choice':
        final on = values.picked[f.name];
        if (f.choices.length <= 3 && f.choices.every((c) => '$c'.length <= 12)) {
          return TkKitField(
            label: _label(f),
            hint: f.hint,
            field: false,
            child: TkChips<Object?>(
              label: f.label,
              padding: EdgeInsets.zero,
              chips: [for (final c in f.choices) (c, '$c')],
              on: on,
              onPick: (v) {
                values.picked[f.name] = v;
                onChanged();
              },
            ),
          );
        }
        return TkKitField(
          label: _label(f),
          hint: f.hint,
          field: false,
          child: TkPickBox(
            label: f.label,
            shown: on == null ? 'Choose…' : '$on',
            onTap: () async {
              final got = await pickOneOf(context, title: f.label, value: on == null ? null : '$on', choices: [for (final c in f.choices) ('$c', '$c', '')]);
              if (got == null) return;
              values.picked[f.name] = f.choices.firstWhere((c) => '$c' == got, orElse: () => got);
              onChanged();
            },
          ),
        );
      case 'device':
        return _device(f);
      case 'scan':
        final read = values.scanned[f.name];
        final typed = values.boxes[f.name]!.text.trim();
        return TkKitField(
          label: _label(f),
          hint: read != null && read.text.trim() == typed ? 'Read by the camera: ${aFormat(read.format)}' : (f.hint ?? f.prop['why'] as String?),
          child: Row(
            children: [
              Expanded(child: TkTextBox(controller: values.boxes[f.name]!, size: 15, height: 46, max: 2000)),
              if (onScan != null) ...[
                const SizedBox(width: 8),
                TkSmallButton(label: 'Scan', icon: 'b-qr', semantics: 'Scan ${f.label}', onPressed: busy ? null : () => onScan!(f)),
              ],
            ],
          ),
        );
      case 'choices':
        final on = values.picked[f.name] as List;
        return TkKitField(
          label: _label(f),
          hint: f.hint,
          field: false,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final c in f.choices)
                Semantics(
                  checked: on.contains(c),
                  child: TkListRow(
                    title: '$c',
                    leading: TkCheckbox(checked: on.contains(c)),
                    onTap: () {
                      on.contains(c) ? on.remove(c) : on.add(c);
                      onChanged();
                    },
                  ),
                ),
            ],
          ),
        );
    }
    final box = TkTextBox(
      controller: values.boxes[f.name]!,
      size: 15,
      height: 46,
      lines: f.kind == 'long' ? 3 : 1,
      money: f.kind == 'money',
      signed: f.kind == 'number',
      prefix: f.kind == 'money' ? '₦' : null,
      phone: f.prop['format'] == 'phone-ng',
      max: f.kind == 'long' ? 2000 : 300,
    );
    final saved = onSaved == null ? null : toolSavedKind(f);
    return TkKitField(
      label: _label(f),
      hint: f.hint,
      error: values.problem(f),
      child: saved == null
          ? box
          : Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: box),
                const SizedBox(width: 8),
                SavedButton(
                  semantics: _savedSemantics(saved, f.label),
                  picking: picking == f.name,
                  busy: busy || (picking != null && picking != f.name),
                  onPressed: () => onSaved!(f, saved),
                ),
              ],
            ),
    );
  }
}

// --- the sheet ---

/// A tool started: its run, and the values as the server read them (the
/// echo, or the summary said Yes to).
typedef MadeStarted = ({String run, List<(String, String)> values});

/// Opens a tool's sheet; it closes with the run it started, or nothing.
/// [initial] fills its form (a button on an answer, with its inputs), and
/// [button] is that button's token, sent with the start.
Future<MadeStarted?> showMadeToolSheet(
  BuildContext context, {
  required Auth auth,
  required MadeTool tool,
  Map<String, dynamic> initial = const {},
  String? words,
  String? button,
  ValueChanged<bool>? onAlwaysAsk,
}) => showTkSheet<MadeStarted>(context, (_) => MadeToolSheet(auth: auth, tool: tool, initial: initial, words: words, button: button, onAlwaysAsk: onAlwaysAsk));

class MadeToolSheet extends StatefulWidget {
  const MadeToolSheet({super.key, required this.auth, required this.tool, this.initial = const {}, this.words, this.button, this.onAlwaysAsk});
  final Auth auth;
  final MadeTool tool;
  final Map<String, dynamic> initial;

  /// Words typed after its command ("/expense 4500 diesel"): read into the
  /// form when it opens (POST /v1/tools/{command}/read), nothing started.
  final String? words;

  /// The button pressed to open it (its token): the start carries it, so a
  /// once button is spent by the run it starts (the confirm keeps it).
  final String? button;

  /// An owner switched "Always ask before it runs".
  final ValueChanged<bool>? onAlwaysAsk;

  @override
  State<MadeToolSheet> createState() => _MadeToolSheetState();
}

class _MadeToolSheetState extends State<MadeToolSheet> {
  late final _values = _FormValues(schemaFields(widget.tool.inputs), widget.initial);
  late bool _alwaysAsk = widget.tool.alwaysAsk;
  bool _busy = false;
  bool _saving = false;

  /// The values that do not fit (422), as the server said them.
  List<String>? _problems;

  /// Waiting for Yes: the start's answer, and the input it was for.
  ToolStart? _confirm;
  Map<String, dynamic>? _sent;

  /// Why Yes did not go (the passkey, an expired confirm).
  String? _error;

  /// One key for the start, so a start sent twice runs once.
  final _key = 'app-tool-${DateTime.now().microsecondsSinceEpoch}';

  /// Fill from words: its box, whether it is being read, and what the
  /// reading said (how many filled, and what could not be read).
  late final _words = TextEditingController(text: widget.words ?? '');
  bool _reading = false;
  String? _readSaid;
  List<String> _readNotes = const [];

  /// The field whose saved things are being picked from.
  String? _picking;

  @override
  void initState() {
    super.initState();
    for (final b in _values.boxes.values) {
      b.addListener(() => setState(() {}));
    }
    _words.addListener(() => setState(() {}));
    if ((widget.words ?? '').trim().isNotEmpty) _read();
  }

  @override
  void dispose() {
    _values.dispose();
    _words.dispose();
    super.dispose();
  }

  /// The words in the box read into the form: the values only fill it;
  /// its own Run it or Next starts anything.
  Future<void> _read() async {
    final words = String.fromCharCodes(_words.text.trim().runes.take(300));
    if (words.isEmpty || _reading) return;
    setState(() => (_reading = true, _readSaid = null, _readNotes = const []));
    try {
      final r = await widget.auth.api.readTool(command: widget.tool.command, body: ReadToolRequest(text: words));
      if (!mounted) return;
      final n = _values.fill(r.values);
      setState(() {
        _readNotes = r.notes;
        _readSaid = n == 0
            ? (r.notes.isEmpty ? 'Nothing in those words fits its fields. Fill them in below.' : null)
            : 'Filled ${n == 1 ? 'one field' : '$n fields'} from your words. Check them before you go on.';
      });
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _readSaid = e is ApiError && e.status == 429 ? 'Too many at once. Wait a moment and try again.' : errorWords(e));
    } finally {
      if (mounted) setState(() => _reading = false);
    }
  }

  /// A field's Saved button: one of the person's or the business's saved
  /// things of its kind, picked, fills it.
  Future<void> _fromSaved(SchemaField f, String kind) async {
    if (_picking != null) return;
    setState(() => _picking = f.name);
    try {
      final max = (f.prop['maxLength'] as num?)?.toInt() ?? 300;
      final v = await pickSavedFor(context, widget.auth, kind, max: max);
      if (v != null && v.isNotEmpty && mounted) _values.boxes[f.name]?.text = v.length > max ? v.substring(0, max) : v;
    } finally {
      if (mounted) setState(() => _picking = null);
    }
  }

  /// A scan field's Scan: the camera reads its code at the tap, after the
  /// input's why (device fields D2).
  Future<void> _scanInto(SchemaField f) async {
    final got = await codeScanner.scanOne(
      context,
      title: f.label,
      why: f.prop['why'] as String?,
      formats: (f.prop['formats'] as List?)?.whereType<String>().toList(),
      allowTyped: f.prop['allow_typed'] != false,
      backLabel: 'Back to ${widget.tool.name}',
    );
    if (got == null || !mounted) return;
    setState(() {
      _values.scanned[f.name] = got;
      _values.boxes[f.name]?.text = got.text;
    });
  }

  /// A location, a photo or a signature input (build-plan-gaps H3): the
  /// phone asked now, at the tap, after the input's why, as a form step's
  /// are (form_request.dart): the location once, the camera (never the
  /// gallery), the pad.
  Future<void> _askDevice(SchemaField f) async {
    final kind = f.prop['type'];
    final why = f.prop['why'] as String?;
    if (kind == 'location') {
      try {
        final at = await formPlaceFinder.here();
        if (!mounted) return;
        final now = DateTime.now().toUtc().toIso8601String().split('.').first;
        setState(() => _values.read[f.name] = {'lat': at.lat, 'lng': at.lng, 'accuracy_m': ?at.accuracy?.round(), 'at': '${now}Z', 'fix': 'gps'});
      } on PlaceRefused catch (e) {
        if (!mounted) return;
        setState(
          () => _values.read[f.name] = e.problem == PlaceProblem.refused || e.problem == PlaceProblem.refusedForever
              ? {'state': 'refused', 'reason': 'the phone was not allowed to read where I am'}
              : {'state': 'unavailable', 'reason': 'the phone could not read where I am'},
        );
        showTkToast(context, placeProblemWords(e.problem), error: true);
      }
      return;
    }
    if (kind == 'photo') {
      final p = await takeFormPhoto();
      if (p == null || !mounted) return;
      setState(() => (_values.photos[f.name] = p, _values.read.remove(f.name)));
      return;
    }
    final got = await signOnScreen(context, title: f.label, why: why, askName: f.prop['ask_name'] == true, backLabel: 'Back to ${widget.tool.name}');
    if (got == null || !mounted) return;
    setState(() => (_values.signed[f.name] = got, _values.read.remove(f.name)));
  }

  /// The photos and signatures sent first, each held for this tool's
  /// input (POST /v1/tools/{command}/files), and named by their id.
  Future<void> _sendFiles(Map<String, dynamic> input) async {
    final path = '/v1/tools/${Uri.encodeComponent(widget.tool.command)}/files';
    for (final e in _values.photos.entries) {
      final r = await widget.auth.transport.uploadFiles(path, files: [(field: e.key, bytes: e.value.bytes, filename: 'photo.jpg', type: 'image/jpeg')], fields: {'app': 'app'});
      input[e.key] = {'file': HoldToolFileResponse.fromJson(r! as Map<String, dynamic>).file.id};
    }
    for (final e in _values.signed.entries) {
      final r = await widget.auth.transport.uploadFiles(path, files: [(field: e.key, bytes: e.value.png, filename: 'signature.png', type: 'image/png')], fields: {'app': 'app'});
      input[e.key] = {'file': HoldToolFileResponse.fromJson(r! as Map<String, dynamic>).file.id, 'signed_by': ?e.value.name};
    }
  }

  bool get _asks => _alwaysAsk || widget.tool.care.level != 'answers';

  void _done(String run, List<ToolStartValuesItem> values) {
    if (!mounted) return;
    Navigator.of(context).pop<MadeStarted>((run: run, values: [for (final v in values) (v.label, v.value)]));
  }

  /// Said in plain words, as a toast; a values problem stays on the form.
  void _refused(Object e) {
    if (!mounted) return;
    if (e is ApiError && e.status == 422) {
      final p = ((e.body['problems'] as List?) ?? const []).whereType<String>().toList();
      setState(() => _problems = p.isEmpty ? [e.message] : p);
      return;
    }
    final words = switch (e) {
      ApiError(status: 403) => 'This tool isn’t yours to use. ${_sentence(e.message)}'.trim(),
      ApiError(status: 404) => 'That tool is no longer here. ${_sentence(e.message)}'.trim(),
      ApiError(status: 429) => 'The business has used its runs for now. ${_sentence(e.message)}'.trim(),
      ApiError(status: 409) => _sentence(e.message),
      _ => errorWords(e),
    };
    showTkToast(context, words, error: true);
  }

  Future<void> _start() async {
    setState(() => (_busy = true, _problems = null, _error = null));
    final input = _values.input();
    try {
      await _sendFiles(input);
      final s = await widget.auth.api.startTool(
        command: widget.tool.command,
        body: StartToolRequest(input: input, button: widget.button),
        idempotencyKey: _key,
      );
      if (s.runId != null && s.confirm == null) return _done(s.runId!, s.values);
      if (mounted) setState(() => (_confirm = s, _sent = input));
    } on Object catch (e) {
      _refused(e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  /// Yes: the same values, once; a tool that moves money with the phone's
  /// passkey for this tool alone (tool.start on tool:`<command>`).
  Future<void> _yes() async {
    final c = _confirm!;
    setState(() => (_busy = true, _error = null));
    try {
      Map<String, dynamic>? passkey;
      if (c.care == 'step_up') {
        if (!await passkeys.available()) {
          setState(
            () => _error =
                'This tool moves money, so it takes your passkey, and this phone cannot use one. Run it from a phone or computer that has your passkey.',
          );
          return;
        }
        passkey = await passkeyProof(widget.auth, 'tool.start', 'tool:${widget.tool.command}');
      }
      final r = await widget.auth.api.confirmTool(
        command: widget.tool.command,
        body: ConfirmToolRequest(confirm: c.confirm!, input: _sent, passkey: passkey),
      );
      _done(r.runId, c.values);
    } on PasskeyCancelled {
      // Closed the phone's passkey sheet: it waits for Yes.
    } on ApiError catch (e) {
      if (e.status == 409 || (e.status == 404 && e.message.contains('confirmation'))) {
        // Gone or changed: back to the form, to start again.
        setState(() => _confirm = null);
        _refused(e);
      } else if (e.status == 403 && e.body['step_up'] != null) {
        setState(() => _error = _sentence(e.message));
      } else {
        _refused(e);
      }
    } on Object catch (e) {
      _refused(e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _setAlwaysAsk(bool on) async {
    setState(() => (_alwaysAsk = on, _saving = true));
    try {
      await widget.auth.api.setToolSettings(
        command: widget.tool.command,
        body: SetToolSettingsRequest(alwaysAsk: on),
      );
      widget.onAlwaysAsk?.call(on);
      if (mounted) showTkToast(context, on ? '${widget.tool.name} asks before it runs' : '${widget.tool.name} runs as its steps say');
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _alwaysAsk = !on);
      showTkToast(context, e is ApiError && e.status == 403 ? 'Only an owner changes this.' : 'Not saved. ${errorWords(e)}', error: true);
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) => _confirm != null ? _summary(context) : _form(context);

  Widget _form(BuildContext context) {
    final look = TkLook.of(context);
    final t = TkTheme.tokens(context);
    final shown = _values.fields.where((f) => f.kind != 'other');
    final typed = shown.where((f) => f.kind != 'device');
    return TkFormSheet(
      title: widget.tool.name,
      crumb: '/${widget.tool.command}',
      action: _busy ? 'Starting…' : (_asks ? 'Next' : 'Run it'),
      onAction: _busy || !_values.ready ? null : _start,
      children: [
        if (typed.isNotEmpty)
          TkFormGroup(
            title: 'Fill from words',
            children: [
              Row(
                children: [
                  Expanded(
                    child: Semantics(
                      label: 'Fill from words',
                      child: TkTextBox(controller: _words, size: 15, height: 46, max: 300, hint: 'Say it as you would in the chat'),
                    ),
                  ),
                  const SizedBox(width: 8),
                  TkSmallButton(
                    label: _reading ? 'Reading…' : 'Fill',
                    semantics: 'Fill the form from these words',
                    onPressed: _reading || _words.text.trim().isEmpty ? null : _read,
                  ),
                ],
              ),
              if (_readSaid != null) Text(_readSaid!, style: tkText(size: 13.5, lineHeight: 1.35, color: t.muted)),
              for (final n in _readNotes) Text(n, style: tkText(size: 13.5, weight: 600, lineHeight: 1.35, color: t.text)),
            ],
          ),
        if (shown.isNotEmpty)
          _SchemaForm(values: _values, onChanged: () => setState(() {}), onSaved: _fromSaved, onScan: _scanInto, onDevice: _askDevice, picking: _picking, busy: _busy)
        else
          TkFormGroup(
            children: [
              Text(widget.tool.about.isEmpty ? 'It needs nothing from you.' : widget.tool.about, style: tkText(size: 15, lineHeight: 1.4, color: t.text)),
            ],
          ),
        if (_problems != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: 'Check these',
            sub: _problems!.map(_sentence).join('\n'),
            subLines: 6,
            color: look.orange,
          ),
        if (isOwner(widget.auth))
          TkFormGroup(
            children: [
              TkListRow(
                title: 'Always ask before it runs',
                icon: 'b-shield',
                square: look.violet,
                sub: widget.tool.care.level == 'answers' ? 'It only reads, so it runs at once unless this is on' : 'It already asks: its steps change things',
                titleLines: 2,
                chevron: false,
                trailing: TkSwitch(
                  label: 'Always ask before it runs',
                  on: _alwaysAsk || widget.tool.care.level != 'answers',
                  onChanged: _saving || widget.tool.care.level != 'answers' ? null : _setAlwaysAsk,
                ),
              ),
            ],
          ),
      ],
    );
  }

  Widget _summary(BuildContext context) {
    final look = TkLook.of(context);
    final c = _confirm!;
    final money = c.care == 'step_up';
    final because = widget.tool.care.because ?? const [];
    return TkFormSheet(
      title: widget.tool.name,
      crumb: money ? 'Check it, then confirm with your passkey' : 'Check it before it runs',
      second: 'Change',
      onSecond: _busy ? null : () => setState(() => _confirm = null),
      action: _busy ? 'Starting…' : (money ? 'Yes, with passkey' : 'Yes, run it'),
      actionIcon: money ? 'b-key' : null,
      onAction: _busy ? null : _yes,
      children: [
        TkFormGroup(
          title: 'What will run',
          children: [
            c.values.isEmpty ? TkLines([('Values', 'None')]) : TkLines([for (final v in c.values) (v.label, v.value)]),
          ],
        ),
        if (because.isNotEmpty || money)
          TkCallout(
            leading: TkSquare(money ? 'b-lock' : 'b-check', color: money ? look.orange : look.blue),
            title: money ? 'It moves money out of the business' : 'Why it asks',
            sub: because.isEmpty ? null : because.map(_sentence).join('\n'),
            subLines: 4,
            color: money ? look.orange : look.blue,
          ),
        if (_error != null)
          TkCallout(
            leading: TkSquare('b-x', color: look.red),
            title: 'Not started',
            sub: _error,
            subLines: 4,
            color: look.red,
          ),
      ],
    );
  }
}

/// The server's words as a sentence: a capital, a full stop.
String _sentence(String s) {
  final w = s.trim();
  if (w.isEmpty) return w;
  final up = w[0].toUpperCase() + w.substring(1);
  return RegExp(r'[.!?]$').hasMatch(up) ? up : '$up.';
}

// --- the runs and their answers ---

/// A tool's run started from here, and its answer as last read.
class MadeRun {
  MadeRun({required this.id, required this.command, required this.name, required this.values, required this.at, this.answer});

  final String id;
  final String command;
  final String name;
  final List<(String, String)> values;
  final DateTime at;
  ToolAnswer? answer;

  /// Read once and not found (not this person's, or gone).
  bool lost = false;

  /// Buttons pressed that work once.
  final pressed = <String>{};

  /// Still going: read again later.
  bool get going => !lost && (answer == null || const {'running', 'queued', 'waiting', 'pending'}.contains(answer!.status));

  Map<String, dynamic> toJson() => {
    'id': id,
    'command': command,
    'name': name,
    'values': [
      for (final v in values) [v.$1, v.$2],
    ],
    'at': at.toUtc().toIso8601String(),
    if (answer != null && !going) 'answer': answer!.toJson(),
  };

  static MadeRun? fromJson(Object? j) {
    if (j is! Map) return null;
    try {
      return MadeRun(
        id: j['id'] as String,
        command: j['command'] as String,
        name: j['name'] as String,
        values: [for (final v in (j['values'] as List)) ('${(v as List)[0]}', '${v[1]}')],
        at: DateTime.parse(j['at'] as String),
        answer: j['answer'] is Map ? ToolAnswer.fromJson((j['answer'] as Map).cast<String, dynamic>()) : null,
      );
    } on Object {
      return null;
    }
  }
}

/// What a person ran here, kept while the app runs (and on the phone, when
/// it keeps the conversation), each read again gently until it ends.
final _kept = Expando<List<MadeRun>>();

class MadeRuns extends ChangeNotifier {
  MadeRuns(this.auth, {DateTime Function()? now}) : _now = now ?? DateTime.now, runs = _kept[auth] ??= _restore(auth) {
    for (final r in runs.where((r) => r.going)) {
      unawaited(_read(r));
    }
  }

  static List<MadeRun> _restore(Auth auth) {
    try {
      final j = auth.local?.read('taskiem_tool_runs');
      if (j is List) return j.map(MadeRun.fromJson).whereType<MadeRun>().toList();
    } on Object {
      // Kept by an older app: the conversation stands without them.
    }
    return <MadeRun>[];
  }

  final Auth auth;
  final DateTime Function() _now;
  final List<MadeRun> runs;
  final _timers = <String, Timer>{};
  bool _disposed = false;

  void add(MadeRun r) {
    runs.add(r);
    if (runs.length > 30) runs.removeAt(0);
    _keep();
    _changed();
    unawaited(_read(r));
  }

  void _keep() {
    try {
      auth.local?.write('taskiem_tool_runs', [for (final r in runs) r.toJson()]);
    } on Object {
      // Kept only while the app runs.
    }
  }

  /// Gently: every second and a half at first, every four seconds after
  /// ten, every fifteen after a minute; a run waiting on a person, every
  /// thirty.
  Duration _after(MadeRun r) {
    final s = _now().difference(r.at).inSeconds;
    if (r.answer?.status == 'waiting') return const Duration(seconds: 30);
    if (s < 10) return const Duration(milliseconds: 1500);
    if (s < 60) return const Duration(seconds: 4);
    return const Duration(seconds: 15);
  }

  Future<void> _read(MadeRun r) async {
    _timers.remove(r.id)?.cancel();
    if (_disposed) return;
    try {
      r.answer = await auth.api.getRunAnswer(run: r.id);
    } on ApiError catch (e) {
      if (e.status == 404 || e.status == 403) r.lost = true;
    } on Object {
      // Offline: read again later.
    }
    if (_disposed) return;
    if (!r.going) _keep();
    _changed();
    if (r.going) _timers[r.id] = Timer(_after(r), () => _read(r));
  }

  void pressed(MadeRun r, String key) {
    r.pressed.add(key);
    _changed();
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    for (final t in _timers.values) {
      t.cancel();
    }
    _timers.clear();
    super.dispose();
  }
}

/// The run's answer as the chat message the server also keeps for it
/// (api/tools_chat.go toolAnswerWords): how its copy in the history is
/// known, so it is not shown twice.
String? answerWords(ToolAnswer a) {
  switch (a.type) {
    case 'text':
      return a.text;
    case 'card':
      final lines = ['*${a.title ?? ''}*${(a.ref ?? '').isNotEmpty ? ' · ${a.ref}' : ''}'];
      if ((a.state?.text ?? '').isNotEmpty) lines.add(a.state!.text);
      for (final l in a.lines ?? const <ToolAnswerLinesItem>[]) {
        lines.add('${l.label}: ${l.value}');
      }
      return lines.join('\n');
    case 'list':
      final rows = a.rows ?? const [];
      if (rows.isEmpty) return null;
      final first = '• ${rows.first.title}${(rows.first.sub ?? '').isNotEmpty ? ' · ${rows.first.sub}' : ''}';
      return (a.title ?? '').isNotEmpty ? '*${a.title}*\n$first' : first;
  }
  return null;
}

/// Whether a message of Taskiem's is the history's copy of [r]'s answer or
/// outcome: its link to the run, or its words.
bool isAnswerCopy(MadeRun r, String text, DateTime at) {
  if (at.isBefore(r.at.subtract(const Duration(minutes: 1)))) return false;
  if (text.contains('/runs/${r.id}')) return true;
  final a = r.answer;
  if (a == null || r.going) return false;
  final w = answerWords(a);
  if (w != null && w.trim().isNotEmpty && text.contains(w.trim())) return true;
  final e = a.error ?? '';
  return a.status == 'failed' && e.isNotEmpty && text.contains(e);
}

/// The tone a workflow gave a state, as the app's pills have them.
CardTone answerTone(String? tone) => switch (tone?.toLowerCase()) {
  'warn' || 'warning' || 'orange' || 'amber' => CardTone.warn,
  'good' || 'ok' || 'lime' || 'green' || 'done' || 'success' => CardTone.lime,
  'danger' || 'bad' || 'red' || 'error' || 'failed' => CardTone.danger,
  _ => CardTone.plain,
};

/// "Waiting for Ada to approve", "Waiting for your answer: open To fill
/// in": one sentence a step the run waits on (made tools §7).
String waitWords(ToolAnswerWait w) => w.kind == 'approval'
    ? (w.you == true ? 'Waiting for you to approve: open Approvals.' : 'Waiting for ${w.who} to approve.')
    : (w.you == true ? 'Waiting for your answer: open To fill in.' : 'Waiting for ${w.who} to answer.');

/// The words of a run that has no answer to show (yet): working, queued,
/// waiting (for whom, when the server says), stopped, done.
String? runWordsOf(MadeRun r) {
  final a = r.answer;
  if (r.lost) return 'That run can’t be read any more.';
  if (a == null) return 'Working on it…';
  final waits = a.waitingFor ?? const <ToolAnswerWait>[];
  if (waits.isNotEmpty && const {'running', 'pending', 'waiting'}.contains(a.status)) return waits.map(waitWords).join(' ');
  return switch (a.status) {
    'running' || 'pending' => 'Working on it…',
    'queued' => 'Queued behind other work. I’ll tell you when it’s done.',
    'waiting' => 'Waiting for someone to approve or answer it. I’ll tell you when it’s done.',
    'cancelled' => '${r.name} was stopped before it finished.',
    'needs_reconciliation' => '${r.name} stopped part-way and needs checking.',
    'failed' => null,
    'completed' when a.type == 'outcome' => '${r.name} is done.',
    'completed' when a.type == 'text' => a.text ?? '${r.name} is done.',
    _ => null,
  };
}

/// What a card's or a row's button does: pressed by its token, opens the
/// sheet of the tool the server names, filled with the values it gives.
typedef OnAnswerButton = void Function(MadeRun run, ToolButton b, String key);

/// The buttons that can be pressed: those the server minted (one without
/// its token is left out), each with its place among them all.
List<(int, ToolButton)> pressableButtons(List<ToolButton>? bs) => [
  for (final (i, b) in (bs ?? const <ToolButton>[]).indexed)
    if ((b.token ?? '').isNotEmpty) (i, b),
];

/// Why a button did not open its tool, and whether it is spent (greyed):
/// someone was first (the server's words: "Already done: Ada took it at
/// 10:42 on 22 Nov."), or it is gone.
({String words, bool spent}) pressRefused(Object e) => switch (e) {
  ApiError(status: 409) => (words: _sentence(e.message), spent: true),
  ApiError(status: 404) => (words: 'This button is not available any more.', spent: true),
  ApiError(status: 403) => (words: 'This tool isn’t yours to use. ${_sentence(e.message)}'.trim(), spent: false),
  _ => (words: errorWords(e), spent: false),
};

/// A run's answer that is more than words: a card, a list, or how it
/// failed, in the chat's card frame.
class MadeAnswerCard extends StatelessWidget {
  const MadeAnswerCard({super.key, required this.run, required this.onButton, required this.auth, this.shownRows = 5});
  final MadeRun run;
  final Auth auth;
  final OnAnswerButton onButton;

  /// How many of a list's rows show in the chat; the rest on its page.
  final int shownRows;

  @override
  Widget build(BuildContext context) {
    final a = run.answer!;
    final failed = a.status == 'failed';
    return LayoutBuilder(
      builder: (context, box) => Align(
        alignment: Alignment.centerLeft,
        child: Container(
          width: (box.maxWidth * 0.86).clamp(0.0, 420.0),
          padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
          decoration: cardDecoration(context, mine: false, first: true),
          child: failed ? _failed(context, a) : (a.type == 'list' ? _list(context, a) : _card(context, a)),
        ),
      ),
    );
  }

  Widget _kicker(BuildContext context, String words, ToolAnswerState? st, {CardTone? tone, String? pill}) {
    final t = TkTheme.tokens(context);
    final p = pill ?? st?.text ?? '';
    return Row(
      children: [
        Expanded(
          child: Text(
            words.toUpperCase(),
            semanticsLabel: words,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: tkText(size: 11, weight: 700, letterSpacing: 0.66, color: t.muted),
          ),
        ),
        if (p.isNotEmpty) ...[const SizedBox(width: 8), Flexible(child: CardPill(p, tone ?? answerTone(st?.tone)))],
      ],
    );
  }

  List<Widget> _spaced(List<Widget> children) => [
    for (var i = 0; i < children.length; i++) ...[if (i > 0) SizedBox(height: i == 1 ? 6 : 4), children[i]],
  ];

  Widget _buttons(List<ToolButton> bs, String at) => CardActionRows(
    children: [
      for (final (i, b) in pressableButtons(bs))
        () {
          final key = '$at:$i';
          final used = run.pressed.contains(key);
          final button = CardButton(label: b.label, primary: i == 0, onTap: used ? null : () => onButton(run, b, key));
          // Used, or gone: greyed, as the web greys a button it cannot press.
          return used ? Opacity(opacity: 0.45, child: button) : button;
        }(),
    ],
  );

  Widget _card(BuildContext context, ToolAnswer a) {
    final t = TkTheme.tokens(context);
    final title = (a.title ?? '').isEmpty ? run.name : a.title!;
    final amount = a.amountKobo;
    final lines = a.lines ?? const <ToolAnswerLinesItem>[];
    final buttons = [for (final (_, b) in pressableButtons(a.buttons)) b];
    return Semantics(
      container: true,
      label: '${run.name}: $title',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: _spaced([
          _kicker(context, (a.ref ?? '').isNotEmpty ? a.ref! : run.name, a.state),
          if (amount != null) ...[
            Text(naira(amount), style: tkDisplay(size: 26, lineHeight: 1.15, letterSpacing: -0.9, color: t.text)),
            Text(title, style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
          ] else
            Text(title, style: tkText(size: 15.5, weight: 600, color: t.text)),
          if (lines.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 4), child: TkLines([for (final l in lines) (l.label, l.value)])),
          if (buttons.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 6), child: _buttons(a.buttons!, 'card')),
        ]),
      ),
    );
  }

  Widget _list(BuildContext context, ToolAnswer a) {
    final t = TkTheme.tokens(context);
    final rows = a.rows ?? const <ToolAnswerRowsItem>[];
    final title = (a.title ?? '').isEmpty ? run.name : a.title!;
    final more = rows.length > shownRows || rows.any((r) => pressableButtons(r.actions).isNotEmpty);
    return Semantics(
      container: true,
      label: '${run.name}: $title',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: _spaced([
          _kicker(context, run.name, null, pill: rows.isEmpty ? null : (rows.length == 1 ? '1 row' : '${rows.length} rows'), tone: CardTone.plain),
          Text(title, style: tkText(size: 15.5, weight: 600, color: t.text)),
          if (rows.isEmpty)
            Text((a.empty ?? '').isEmpty ? 'Nothing to show.' : a.empty!, style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted))
          else
            Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [for (final (i, r) in rows.take(shownRows).indexed) _row(context, r, i > 0)]),
          if (more)
            CardViewRow(
              label: rows.length > shownRows ? 'See all ${rows.length}' : 'See all',
              semantics: 'See all of $title',
              onTap: () => Navigator.of(context).push(
                TkRoute<void>(
                  pageBuilder: (_, _, _) => MadeListPage(run: run, onButton: onButton),
                ),
              ),
            ),
        ]),
      ),
    );
  }

  Widget _row(BuildContext context, ToolAnswerRowsItem r, bool line) {
    final t = TkTheme.tokens(context);
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 8),
      decoration: BoxDecoration(
        border: line ? Border(top: BorderSide(color: TkLook.of(context).line)) : null,
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  r.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: tkText(size: 14, weight: 600, color: t.text),
                ),
                if ((r.sub ?? '').isNotEmpty)
                  Text(
                    r.sub!,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: tkText(size: 12.5, color: t.muted),
                  ),
              ],
            ),
          ),
          if ((r.state?.text ?? '').isNotEmpty) ...[const SizedBox(width: 8), CardPill(r.state!.text, answerTone(r.state!.tone))],
        ],
      ),
    );
  }

  Widget _failed(BuildContext context, ToolAnswer a) {
    final t = TkTheme.tokens(context);
    final why = (a.error ?? '').isEmpty ? 'It stopped before it finished.' : _sentence(a.error!);
    return Semantics(
      container: true,
      label: '${run.name} failed',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: _spaced([
          _kicker(context, run.name, null, pill: 'Failed', tone: CardTone.danger),
          Text('${run.name} didn’t finish', style: tkText(size: 15.5, weight: 600, color: t.text)),
          Text(why, style: tkText(size: 13.5, lineHeight: 1.4, color: t.muted)),
          CardViewRow(
            label: 'See the run',
            semantics: 'See the run of ${run.name}',
            onTap: () => openOrSay(
              context,
              () => openInBrowser(Uri.parse('${auth.transport.base}/runs/${Uri.encodeComponent(run.id)}')),
              'The run could not be opened.',
            ),
          ),
        ]),
      ),
    );
  }
}

/// A list answer's every row and its buttons ("See all").
class MadeListPage extends StatelessWidget {
  const MadeListPage({super.key, required this.run, required this.onButton});
  final MadeRun run;
  final OnAnswerButton onButton;

  @override
  Widget build(BuildContext context) {
    final a = run.answer!;
    final rows = a.rows ?? const <ToolAnswerRowsItem>[];
    final look = TkLook.of(context);
    return TkAppLook(
      child: TkPageScreen(
        title: (a.title ?? '').isEmpty ? run.name : a.title!,
        backLabel: 'Back to Taskiem',
        titleSize: 30,
        children: [
          TkSection(
            title: rows.length == 1 ? '1 row' : '${rows.length} rows',
            inset: 56,
            empty: (a.empty ?? '').isEmpty ? 'Nothing to show.' : a.empty,
            children: [
              for (final (i, r) in rows.indexed)
                () {
                  // Each with its place among the row's buttons; one
                  // used (or gone) is not offered again.
                  final acts = [
                    for (final (j, b) in pressableButtons(r.actions))
                      if (!run.pressed.contains('row$i:$j')) (j, b),
                  ];
                  return TkListRow(
                    title: r.title,
                    sub: [r.sub ?? '', if (acts.isNotEmpty) acts.map((a) => a.$2.label).join(' · ')].where((s) => s.isNotEmpty).join('\n'),
                    icon: madeIconFor(run),
                    square: acts.isEmpty ? look.blue : look.teal,
                    status: (r.state?.text ?? '').isEmpty ? null : r.state!.text,
                    statusTone: answerTone(r.state?.tone),
                    chevron: acts.isNotEmpty,
                    onTap: acts.isEmpty
                        ? null
                        : () async {
                            final key = acts.length == 1
                                ? '${acts.single.$1}'
                                : await pickAction(context, title: r.title, actions: [for (final (j, b) in acts) ('$j', b.label, 'b-bolt', false)]);
                            if (key == null || !context.mounted) return;
                            final j = int.parse(key);
                            Navigator.of(context).pop();
                            onButton(run, acts.firstWhere((a) => a.$1 == j).$2, 'row$i:$j');
                          },
                  );
                }(),
            ],
          ),
        ],
      ),
    );
  }
}

/// A row's icon on the list page.
String madeIconFor(MadeRun r) => 'b-bolt';
