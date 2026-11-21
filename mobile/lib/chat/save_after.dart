// Saving after an action (saved objects S3, docs/spec/saved-objects.md
// §9.3: after a new customer's first order, after a delivery to a new
// address): offered once, quietly, saved with one tap; suggested, never
// done for them. A name already taken asks for another in a small sheet.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show SavedApi, cleanSavedName, savedNameProblem;
import 'saved_picks.dart' show SavedPicks;

/// The letters that come apart into a plain letter and its accents
/// (Latin-1, Latin Extended A and B and Additional: "Ọ" of Ọlá, "ẹ" of
/// Ìfẹ́, "ṣ", "ń", "ǹ", "ḿ"), each beside the plain letter it keeps, as
/// Unicode's NFD splits them: Dart has none built in.
const _accented =
    'ÀÁÂÃÄÅÇÈÉÊËÌÍÎÏÑÒÓÔÕÖÙÚÛÜÝàáâãäåçèéêëìíîïñòóôõöùúûüýÿĀāĂăĄąĆćĈĉĊċČčĎďĒēĔĕĖėĘęĚěĜĝĞğĠġĢģĤĥĨĩĪīĬĭĮįİĴĵĶķĹĺĻļĽľŃńŅņŇňŌōŎŏŐőŔŕŖŗŘřŚśŜŝŞşŠšŢţŤťŨũŪūŬŭŮůŰűŲųŴŵŶŷŸŹźŻżŽžƠơƯưǍǎǏǐǑǒǓǔǕǖǗǘǙǚǛǜǞǟǠǡǦǧǨǩǪǫǬǭǰǴǵǸǹǺǻȀȁȂȃȄȅȆȇȈȉȊȋȌȍȎȏȐȑȒȓȔȕȖȗȘșȚțȞȟȦȧȨȩȪȫȬȭȮȯȰȱȲȳḀḁḂḃḄḅḆḇḈḉḊḋḌḍḎḏḐḑḒḓḔḕḖḗḘḙḚḛḜḝḞḟḠḡḢḣḤḥḦḧḨḩḪḫḬḭḮḯḰḱḲḳḴḵḶḷḸḹḺḻḼḽḾḿṀṁṂṃṄṅṆṇṈṉṊṋṌṍṎṏṐṑṒṓṔṕṖṗṘṙṚṛṜṝṞṟṠṡṢṣṤṥṦṧṨṩṪṫṬṭṮṯṰṱṲṳṴṵṶṷṸṹṺṻṼṽṾṿẀẁẂẃẄẅẆẇẈẉẊẋẌẍẎẏẐẑẒẓẔẕẖẗẘẙẠạẢảẤấẦầẨẩẪẫẬậẮắẰằẲẳẴẵẶặẸẹẺẻẼẽẾếỀềỂểỄễỆệỈỉỊịỌọỎỏỐốỒồỔổỖỗỘộỚớỜờỞởỠỡỢợỤụỦủỨứỪừỬửỮữỰựỲỳỴỵỶỷỸỹKÅ';
const _plain =
    'AAAAAACEEEEIIIINOOOOOUUUUYaaaaaaceeeeiiiinooooouuuuyyAaAaAaCcCcCcCcDdEeEeEeEeEeGgGgGgGgHhIiIiIiIiIJjKkLlLlLlNnNnNnOoOoOoRrRrRrSsSsSsSsTtTtUuUuUuUuUuUuWwYyYZzZzZzOoUuAaIiOoUuUuUuUuUuAaAaGgKkOoOojGgNnAaAaAaEeEeIiIiOoOoRrRrUuUuSsTtHhAaEeOoOoOoOoYyAaBbBbBbCcDdDdDdDdDdEeEeEeEeEeFfGgHhHhHhHhHhIiIiKkKkKkLlLlLlLlMmMmMmNnNnNnNnOoOoOoOoPpPpRrRrRrRrSsSsSsSsSsTtTtTtTtUuUuUuUuUuVvVvWwWwWwWwWwXxXxYyZzZzZzhtwyAaAaAaAaAaAaAaAaAaAaAaAaEeEeEeEeEeEeEeEeIiIiOoOoOoOoOoOoOoOoOoOoOoOoUuUuUuUuUuUuUuYyYyYyYyKA';
final _fold = {for (var i = 0; i < _accented.length; i++) _accented.codeUnitAt(i): _plain[i]};

/// A name made from a title, as the server makes one (engine/saved
/// NameFor): accents taken off ("Ọlá Stores" → ola-stores, "Ìfẹ́" → ife),
/// lowercase letters and digits, every other run one "-", at most 32; ''
/// when shorter than 2 ("Bola Ade" → bola-ade).
String savedNameFor(String title) {
  final b = StringBuffer();
  var dash = false;
  for (final r in title.runes) {
    // A combining mark (U+0300–U+036F): the accent of the letter before.
    if (r >= 0x300 && r <= 0x36F) continue;
    final c = (_fold[r] ?? (r < 0x80 ? String.fromCharCode(r) : '')).toLowerCase();
    if (c.length == 1 && RegExp('[a-z0-9]').hasMatch(c)) {
      if (dash && b.isNotEmpty) b.write('-');
      b.write(c);
      dash = false;
    } else {
      dash = true;
    }
  }
  var n = b.toString();
  if (n.length > 32) n = n.substring(0, 32).replaceFirst(RegExp(r'-+$'), '');
  return n.length < 2 ? '' : n;
}

/// The most a saved place's line holds (the server's; an address may be
/// 300).
const placeLineMax = 200;

/// A typed address cut to a place's line: whole, when it fits; else at the
/// last ", " or space before [placeLineMax] characters.
String placeLineFor(String address) {
  final a = address.trim();
  if (a.length <= placeLineMax) return a;
  final head = a.substring(0, placeLineMax);
  var at = head.lastIndexOf(', ');
  if (at <= 0) at = head.lastIndexOf(' ');
  return (at > 0 ? head.substring(0, at) : head).replaceFirst(RegExp(r'[\s,]+$'), '');
}

/// A typed address as a place's title: its first part ("12 Admiralty Way"
/// of "12 Admiralty Way, Lekki"), else "Delivery place".
String placeTitleFor(String address) {
  final first = address.split(RegExp(r'[,\n(]')).first.trim();
  if (first.isEmpty || savedNameFor(first).isEmpty) return 'Delivery place';
  return first.length > 60 ? first.substring(0, 60).trimRight() : first;
}

/// Whether the server needs a name given: none was, and none could be made
/// (a hidden name, or one of no plain letters, as "Ìfẹ́" alone).
bool nameNeeded(ApiError e) => e.has('name_needed') || e.body['code'] == 'name_needed';

/// The answers that end the offer, not ask for a name (not a customer,
/// nothing to save, saved already): thrown on from the name's sheet.
bool _endsOffer(ApiError e) => e.status == 409 && const {'not_a_customer', 'nothing_to_save', 'saved_already'}.contains(e.body['code']);

/// Saves with [name] ('' for the server's own), and when the name is taken
/// asks for another in a sheet and saves with it; when none could be made,
/// asks for one in the same sheet, empty. Null when put away. Other
/// failures are thrown.
Future<SavedObject?> saveOrAskName(BuildContext context, {required String name, required Future<SavedObject> Function(String? name) save}) async {
  var needed = false;
  try {
    return await save(name.isEmpty ? null : name);
  } on ApiError catch (e) {
    needed = nameNeeded(e);
    if (!e.has('name_taken') && !needed) rethrow;
  }
  if (!context.mounted) return null;
  ApiError? ended;
  final x = await showTkSheet<SavedObject>(context, (_) => SaveNameSheet(taken: needed ? '' : name, needed: needed, save: save, ended: (e) => ended = e));
  if (ended case final e?) throw e;
  return x;
}

/// The business's saved things, POST /v1/saved: one place, its line cut to
/// what a place holds.
Future<SavedObject> savePlace(Auth auth, {required String name, required String title, required String address, String phone = ''}) async =>
    (await SavedApi(auth, false).add(SavedObjectIn(name: name, title: title, kind: 'place', details: {'line': placeLineFor(address), if (phone.isNotEmpty) 'phone': phone})))
        .saved;

/// Something was saved: the forms' Saved buttons and the chats' pickers
/// read their lists again.
void savedNow() => SavedPicks.saves++;

/// The name is taken, or none could be made: one typed ("-" between
/// words), and saved with it.
class SaveNameSheet extends StatefulWidget {
  const SaveNameSheet({super.key, required this.taken, required this.save, this.needed = false, this.ended});

  /// The name that was taken ('' when the server made it, or none could
  /// be made).
  final String taken;

  /// None could be made: the sheet asks for one, empty.
  final bool needed;
  final Future<SavedObject> Function(String? name) save;

  /// An answer that ends the offer (not a customer, saved already): the
  /// sheet closes and it is handed back.
  final void Function(ApiError e)? ended;

  @override
  State<SaveNameSheet> createState() => _SaveNameSheetState();
}

class _SaveNameSheetState extends State<SaveNameSheet> {
  late final _name = TextEditingController(text: widget.taken.isEmpty ? '' : '@${widget.taken}');
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  Future<void> _go() async {
    final problem = savedNameProblem(_name.text);
    if (problem != null) return setState(() => _error = problem);
    setState(() => (_busy = true, _error = null));
    try {
      final x = await widget.save(cleanSavedName(_name.text));
      if (mounted) Navigator.of(context).pop(x);
    } on ApiError catch (e) {
      if (_endsOffer(e) && widget.ended != null) {
        widget.ended!(e);
        if (mounted) Navigator.of(context).pop();
        return;
      }
      if (mounted) setState(() => (_busy = false, _error = e.has('name_taken') ? 'That name is taken too: choose another.' : e.message));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkFormSheet(
      title: widget.needed ? 'Give it a name to call it up by' : 'Another name',
      crumb: widget.needed ? null : (widget.taken.isEmpty ? 'That name is taken' : '@${widget.taken} is taken'),
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy || cleanSavedName(_name.text).isEmpty ? null : _go,
      second: 'Not now',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Name to call it up by',
              hint: 'Letters and numbers, with "-" between words',
              child: TkTextBox(controller: _name, max: 33, plain: true),
            ),
          ],
        ),
        if (_error != null)
          TkCallout(
            leading: TkSquare('b-warn', color: look.red),
            title: _error!,
            color: look.red,
          ),
      ],
    );
  }
}
