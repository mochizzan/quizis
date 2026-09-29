# Spesifikasi Desain & Panduan Rekonstruksi UI: ProfitPulse Dashboard

Dokumen ini berisi analisis detail komponen, tata letak, warna, tipografi, serta interaksi antarmuka dari screenshot antarmuka manajemen pesanan **ProfitPulse**. Spesifikasi ini dibuat agar pengembang (developer) atau AI/LLM dapat merekonstruksi ulang tampilan ini secara presisi tanpa perlu melihat gambar asli.

---

## 1. Ikhtisar & Konsep Desain (Design Overview)

- **Aplikasi**: ProfitPulse (Dashboard Manajemen E-Commerce / Pesanan)
- **Tema Visual**: Clean, Modern Minimalist dengan warna latar krem hangat (warm off-white/beige), aksen *dark charcoal/navy*, serta badge berwarna pastel.
- **Gaya Desain**: Card-based modern UI, rounded corner lembut (*large border-radius*), pemisah antar-elemen minimalis tanpa garis border keras (menggunakan warna latar & spacing sebagai pemisah visual).

---

## 2. Arsitektur Tata Letak (Layout Architecture)

Antarmuka terdiri dari **3 Kolom Utama** dalam satu kontainer aplikasi ber-corner radius besar (~24px) atau mode layar penuh:

```
+-------------------+---------------------------------------+---------------------+
| LEFT SIDEBAR      | MAIN CONTENT (ORDERS LIST)            | RIGHT PANEL         |
| Width: ~220-250px | Flex / Expanded                       | Width: ~320-360px   |
| Dark Theme        | Light Cream Theme                     | White Card Overlay  |
|                   | - Top Header                          | - Order Detail      |
| - Logo & Brand    | - Filter & Sort Bar                   | - Customer Info     |
| - Nav Menu Items  | - Data Table / Orders List            | - Itemized Products |
| - System Nav      |   (Termasuk Row Highlighted Active)   | - Total & Actions   |
| - Logout          |                                       |                     |
+-------------------+---------------------------------------+---------------------+
```

1. **Sidebar Kiri (Left Navigation Bar)**:
   - Lebar konstan (~240px).
   - Latar belakang sangat gelap (*dark charcoal*).
   - Berisi logo, navigasi utama, kelompok fungsi sistem, dan tombol kelur (logout) di bagian bawah.
2. **Konten Utama (Middle Column - Orders Page)**:
   - Lebar fleksibel (*flex-grow*).
   - Latar belakang krem/warm off-white.
   - Terdiri dari: Top Header (Judul Halaman & User Profile/Actions), Filter Bar, serta Tabel Daftar Pesanan.
3. **Panel Detail Pesanan Kiri/Kanan (Right Drawer / Detail Card)**:
   - Lebar konstan (~340px).
   - Latar belakang putih solid dengan *border-radius* melengkung halus dan *shadow* lembut.
   - Menampilkan rincian pesanan yang sedang dipilih.

---

## 3. Sistem Desain & Token Visual (Design Tokens)

### 3.1 Palet Warna (Color Palette)

| Kategori | Nama / Peran Warna | Hex Code (Perkiraan Presisi) | Penggunaan |
| :--- | :--- | :--- | :--- |
| **Backgrounds** | Main Canvas Background | `#F5F3EC` / `#F6F4EE` | Latar belakang area konten utama |
| | Sidebar Dark | `#12161A` / `#13181E` | Latar belakang sidebar navigasi |
| | Panel / Card Surface | `#FFFFFF` | Latar belakang modul detail & row aktif |
| **Text Colors** | Primary Text (Dark) | `#111827` / `#1A1D20` | Teks judul, angka, nama pelanggan |
| | Secondary / Muted Text | `#6B7280` / `#8A94A6` | Teks ID order, email, label tanggal |
| | Sidebar Inactive Text | `#8A94A6` | Menu sidebar yang tidak aktif |
| **Status Badges** | `Paid` Background | `#FFF3A8` / `#FEF08A` | Badge status Lunas |
| | `Paid` Text | `#6B5900` | Teks badge Lunas |
| | `Delivered` Background | `#FFD8BE` / `#FDBA74` | Badge status Terkirim |
| | `Delivered` Text | `#7C2D12` | Teks badge Terkirim |
| | `Completed` Background | `#A7F3D0` / `#86EFAC` | Badge status Selesai |
| | `Completed` Text | `#065F46` | Teks badge Selesai |
| **Interactive** | Active Nav Background | `#FFFFFF` | Capsule background menu terpilih di sidebar |
| | Checkbox Checked | `#12161A` | Kotak centang aktif (hitam centang putih) |
| | Primary Button Dark | `#12161A` | Tombol `Track` |
| | Secondary Button Yellow | `#FFF3A8` | Tombol `Refund` |

### 3.2 Tipografi (Typography)

- **Font Family**: Modern Sans-Serif (misal: *Inter*, *Plus Jakarta Sans*, atau *SF Pro Display*).
- **Hierarki Hirarki**:
  - **Judul Halaman ("Orders")**: ~24px, Bold, `#111827`.
  - **Judul Panel ("Order #390561")**: ~18px–20px, Bold.
  - **Sub-header / Nama User ("James Miller")**: ~15px–16px, Semi-Bold.
  - **Body / Row Text**: ~13px–14px, Regular / Medium.
  - **Label / Badges / Subtext**: ~11px–12px, Medium / Regular.

### 3.3 Border Radius & Shadows

- **Main Container Frame**: `24px` (`rounded-3xl`)
- **Detail Panel Card**: `20px` (`rounded-2xl`)
- **Active Row Highlight**: `16px` (`rounded-xl`)
- **Active Nav Item / Pill Buttons**: `9999px` (Full Rounded / Pill)
- **Status Badges**: `8px`–`12px`
- **Product Thumbnails**: `12px`
- **Shadows**: Soft drop shadow pada Panel Detail dan Active Row (`box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.04)`).

---

## 4. Rincian Komponen UI (Detailed Component Breakdown)

### 4.1 Sidebar Navigasi Kiri (Left Sidebar)
- **Header**: Logo berupa ikon grafik/pulse line di dalam lingkaran/petak, diikuti teks merek `ProfitPulse` (Bold, Putih).
- **Grup Navigasi Atas**:
  - `Dashboard` (Ikon Rumah/Grid)
  - `Orders` (**Active State**: latar belakang putih kapsul, teks & ikon hitam `#12161A`, rounded-full)
  - `Payments` (Ikon Kartu Kredit)
  - `Customers` (Ikon Pengguna/Orang)
  - `Reports` (Ikon Dokumen/Laporan)
  - `Statistic` (Ikon Grafik Batang)
- **Grup Navigasi Tengah**:
  - `Notification` (Ikon Lonceng)
  - `Help` (Ikon Tanda Tanya/Seru Lingkaran)
  - `Settings` (Ikon Roda Gigi / Gear)
- **Grup Navigasi Bawah**:
  - `Log out` (Ikon Keluar / Arrow Left Door)

### 4.2 Header Utama & Bar Filter (Middle Main Content Header)
- **Top Header Line**:
  - Kiri: Teks `Orders` besar & bold.
  - Kanan:
    - Tombol Ikon Kotak Pesan/Email (latar belakang putih/krem transparan)
    - Tombol Ikon Pencarian (Pencarian/Magnifier)
    - User Profile Stack: Avatar melingkar (wanita rambut pirang), Nama `Kristina Evans`, Email `kris.evans@gmail.com` di bawahnya.
- **Filter Bar Line**:
  - Dropdown Filter 1: `Any status` [V] (Kapsul putih/krem dengan panah bawah)
  - Dropdown Filter 2: `$100 – $1500` [V] (Kapsul putih/krem dengan panah bawah)
  - Right Align Sort: `Sort by Date` [V]

### 4.3 Tabel / Daftar Pesanan (Orders Data Table)
- **Header Tabel**:
  - Checkbox Master: Lingkaran/persegi gelap dengan garis minus (`-`) melambangkan seleksi parsial.
  - Kolom: `Order`, `Customer`, `Status`, `Total`, `Date`, dan kolom aksi `...`.
- **Baris Data (13 Baris Terlihat)**:
  1. `[ ]` | `#390561` | Michelle Black | `Paid` (Kuning) | `$780.00` | Jan 8 | `...`
  2. `[ ]` | `#663334` | Janice Chandler | `Delivered` (Peach) | `$1,250.00` | Jan 6 | `...`
  3. `[x]` | `#418135` | Mildred Hall | `Paid` (Kuning) | `$540.95` | Jan 5 | `...`
  4. `[ ]` | `#801999` | Ana Carter | `Paid` (Kuning) | `$1,489.00` | Jan 2 | `...`
  5. `[ ]` | `#517783` | John Sherman | `Completed` (Hijau) | `$925.00` | Dec 28 | `...`
  6. **`[x]` | `#602992` | James Miller | `Paid` (Kuning) | `$1,620.00` | Dec 26 | `...`** *(Active Row: Memiliki kartu putih yang menonjol keluar/elevated)*
  7. `[x]` | `#730345` | Travis French | `Paid` (Kuning) | `$315.50` | Dec 22 | `...`
  8. `[ ]` | `#126955` | Ralph Hall | `Paid` (Kuning) | `$1,267.45` | Dec 20 | `...`
  9. `[x]` | `#045321` | Gary Gilbert | `Completed` (Hijau) | `$287.00` | Dec 18 | `...`
  10. `[ ]` | `#082848` | Frances Howell | `Delivered` (Peach) | `$1,740.00` | Dec 17 | `...`
  11. `[ ]` | `#646072` | Herbert Boyd | `Paid` (Kuning) | `$714.00` | Dec 14 | `...`
  12. `[ ]` | `#432019` | Alan White | `Paid` (Kuning) | `$267.65` | Dec 13 | `...`
  13. `[ ]` | `#985927` | Julie Martin | `Delivered` (Peach) | `$389.00` | Dec 11 | `...`

### 4.4 Panel Detail Pesanan (Right Side Drawer)
- **Header Drawer**:
  - Judul: `Order #390561` *(Catatan: ID pada header drawer menampilkan #390561, sementara item detail produk yang dirender di bawahnya cocok dengan total harga baris James Miller $1,620.00)*.
  - Badge Status: `Paid` (Kuning) & Subtext Tanggal `Jan 8, 13:52`.
  - Tombol Silang `X` di sudut kanan atas untuk menutup.
- **Profil Pelanggan**:
  - Foto profil lingkaran besar (pria berkupluk kuning).
  - Nama: `James Miller` (Bold, center).
  - 3 Tombol Aksi Bulat Kecil (Latar abu-abu terang): Ikon Pesan, Ikon Telepon, Ikon WhatsApp.
- **Daftar Barang (Order items)**:
  - Header Seksi: `Order items`
  - Item 1: Thumbnail Bor | `Ryobi ONE drill/driver` | `$409.00`
  - Item 2: Thumbnail Stop Kontak | `Socket Systeme Electric` | `$238.00`
  - Item 3: Thumbnail TV Box | `DVB-T2 receiver bbk` | `$139.00`
  - Item 4: Thumbnail Kompresor | `Inforce oil-free compressor` | `$135.00`
  - Item 5: Thumbnail Mesin Las | `TIG-200 welding inverter` | `$699.00`
- **Ringkasan & Tombol Aksi**:
  - Baris Total: Teks `Total` (kiri) | `$1,620.00` (kanan, bold besar). *(Perhitungan: 409 + 238 + 139 + 135 + 699 = $1,620.00)*.
  - Tombol Aksi Bawah:
    - Tombol Kiri: `Track` (Hitam/Dark Charcoal, ikon kompas/radar, teks putih, kapsul).
    - Tombol Kanan: `Refund` (Kuning muda, ikon panah undol/kembali, teks gelap, kapsul).

---

## 5. UI/UX, State, & Behavior Interaktif

1. **Active Row & Panel Sync**:
   - Ketika baris dalam tabel diklik (pada contoh ini: `James Miller` / `#602992`), baris tersebut berubah menjadi kartu putih terangkat (*elevated white card*) dan membuka **Right Detail Panel** di sisi kanan.
2. **Multi-Selection Checkbox**:
   - Checkbox memungkinkan seleksi item dalam jumlah banyak (bulk operations). Beberapa item dalam contoh gambar tercentang secara individual (`#418135`, `#602992`, `#730345`, `#045321`).
3. **Responsive / Panel Overlay**:
   - Panel kanan bertindak sebagai *fixed drawer* atau *integrated flex column* yang dapat ditutup melalui ikon `X`.

---

## 6. Catatan Rekonstruksi & Asumsi

- **Asumsi ID Order Header Panel**: Terdapat ketidaksesuaian kecil antara ID pada header panel kanan (`#390561`) dengan ID baris terpilih di tabel (`#602992`). Namun total harga ($1,620.00) dan item yang tertera secara matematis persis cocok dengan pesanan James Miller. Disarankan bagi developer untuk menyinkronkan ID secara dinamis berdasarkan baris yang diklik.
- **System Font**: Menggunakan font sans-serif modern standar seperti `Inter` atau `Plus Jakarta Sans` dengan `letter-spacing: -0.01em`.