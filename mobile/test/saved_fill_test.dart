// A form's Saved button (lib/chat/saved_fill.dart): a saved place as an
// address that fits, and the dates saved on things.

import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/api/api.g.dart';
import 'package:taskiem/chat/saved_fill.dart';

import 'saved_stub.dart';

SavedObjectDetails _place({String? line, String? area, String? landmark}) => SavedObjectDetails(line: line, area: area, landmark: landmark);

void main() {
  test('a place as an address line: no “ (landmark)” with no street', () {
    expect(placeLine(_place(line: '12 Admiralty Way', area: 'Lekki', landmark: 'by the filling station')), '12 Admiralty Way, Lekki (by the filling station)');
    expect(placeLine(_place(landmark: 'The blue gate')), 'The blue gate');
    expect(placeLine(_place(area: 'Yaba', landmark: 'The blue gate')), 'Yaba (The blue gate)');
    expect(placeLine(_place()), '');
  });

  test('a long place fits 300 characters: the landmark left out, then the area, then the street cut', () {
    final street = 'S' * 200, area = 'A' * 80, mark = 'M' * 60;
    expect(placeFit(_place(line: street, area: area, landmark: mark), max: 300), (line: '$street, $area', dropped: 'the landmark'));
    expect(placeFit(_place(line: street, area: 'A' * 120, landmark: mark), max: 300), (line: street, dropped: 'the landmark and the area'));
    expect(placeFit(_place(line: street, area: 'A' * 120), max: 300), (line: street, dropped: 'the area'));
    final cut = placeFit(_place(line: 'S' * 320, landmark: mark), max: 300);
    expect((cut.line.length, cut.dropped), (300, 'the end of the address'));
    expect(placeFit(_place(line: 'Short', landmark: 'gate'), max: 300), (line: 'Short (gate)', dropped: ''));
  });

  test('dates saved on things: a made-up kind’s date fields and a person’s next birthday; a hidden one left out', () {
    final kinds = [
      SavedKind.fromJson(savedKind('K1', 'Car', [
        {'key': 'insurance_due', 'label': 'Insurance due', 'type': 'date'},
        {'key': 'bought', 'label': 'Bought', 'type': 'date', 'sensitive': true},
        {'key': 'serviced', 'label': 'Serviced', 'type': 'date'},
        {'key': 'plate', 'label': 'Plate', 'type': 'text'},
      ])),
    ];
    final things = [
      SavedObject.fromJson(savedThing('C1', 'mine', 'custom', 'car', 'The car', '', const {}, kindId: 'K1', values: {'insurance_due': '2027-03-03', 'bought': '••', 'serviced': '••12', 'plate': 'LND 123'})),
      SavedObject.fromJson(savedThing('P1', 'mine', 'person', 'mum', 'Mum', '', {'birthday': '1970-03-21'})),
      SavedObject.fromJson(savedThing('P2', 'mine', 'person', 'dad', 'Dad', '', {'birthday': '1968-12-01'})),
      SavedObject.fromJson(savedThing('X1', 'mine', 'place', 'home', 'Home', '', {'line': '3 Herbert Macaulay Way'})),
    ];
    final got = savedDates(things, kinds, DateTime(2026, 11, 20));
    expect([for (final d in got) '@${d.x.name} · ${d.label} · ${savedDayWords(d.day)}'], [
      '@car · Insurance due · 3 Mar 2027',
      '@mum · Birthday · 21 Mar 2027',
      '@dad · Birthday · 1 Dec 2026',
    ]);
    expect(got.first.day, '2027-03-03');
  });
}
