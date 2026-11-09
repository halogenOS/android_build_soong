// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

//! Reads the layout of a class file: its constant pool, every place that reads a UTF8 entry of
//! the pool together with how that place reads the entry, and the annotations of the class, of
//! its fields and of its methods.

use super::ClassError;
use std::collections::BTreeMap;

/// The tag of a constant pool entry (JVMS 4.4).
#[derive(Clone, Copy)]
#[repr(u8)]
pub(super) enum ConstantTag {
    Utf8 = 1,
    Integer = 3,
    Float = 4,
    Long = 5,
    Double = 6,
    Class = 7,
    String = 8,
    Fieldref = 9,
    Methodref = 10,
    InterfaceMethodref = 11,
    NameAndType = 12,
    MethodHandle = 15,
    MethodType = 16,
    Dynamic = 17,
    InvokeDynamic = 18,
    Module = 19,
    Package = 20,
}

impl ConstantTag {
    const ALL: [Self; 17] = [
        Self::Utf8,
        Self::Integer,
        Self::Float,
        Self::Long,
        Self::Double,
        Self::Class,
        Self::String,
        Self::Fieldref,
        Self::Methodref,
        Self::InterfaceMethodref,
        Self::NameAndType,
        Self::MethodHandle,
        Self::MethodType,
        Self::Dynamic,
        Self::InvokeDynamic,
        Self::Module,
        Self::Package,
    ];

    /// The size of the fixed part of the entry after the tag. The fixed part of a Utf8 entry is
    /// the u2 length of the bytes that follow it.
    fn payload_size(self) -> usize {
        match self {
            Self::Utf8
            | Self::Class
            | Self::String
            | Self::MethodType
            | Self::Module
            | Self::Package => 2,
            Self::MethodHandle => 3,
            Self::Integer
            | Self::Float
            | Self::Fieldref
            | Self::Methodref
            | Self::InterfaceMethodref
            | Self::NameAndType
            | Self::Dynamic
            | Self::InvokeDynamic => 4,
            Self::Long | Self::Double => 8,
        }
    }

    /// The number of pool indices the entry takes: two for the 8-byte constants (JVMS 4.4.5).
    fn slots(self) -> u16 {
        match self {
            Self::Long | Self::Double => 2,
            _ => 1,
        }
    }
}

impl TryFrom<u8> for ConstantTag {
    type Error = ClassError;

    fn try_from(tag: u8) -> Result<Self, ClassError> {
        Self::ALL.into_iter().find(|known| *known as u8 == tag).ok_or(ClassError::BadTag(tag))
    }
}

/// The target type of a type annotation (JVMS 4.7.20.1).
#[derive(Clone, Copy)]
#[repr(u8)]
enum TargetType {
    ClassTypeParameter = 0x00,
    MethodTypeParameter = 0x01,
    Supertype = 0x10,
    ClassTypeParameterBound = 0x11,
    MethodTypeParameterBound = 0x12,
    Field = 0x13,
    MethodReturn = 0x14,
    Receiver = 0x15,
    FormalParameter = 0x16,
    Throws = 0x17,
    LocalVariable = 0x40,
    ResourceVariable = 0x41,
    ExceptionParameter = 0x42,
    Instanceof = 0x43,
    New = 0x44,
    ConstructorReference = 0x45,
    MethodReference = 0x46,
    Cast = 0x47,
    ConstructorInvocationTypeArgument = 0x48,
    MethodInvocationTypeArgument = 0x49,
    ConstructorReferenceTypeArgument = 0x4A,
    MethodReferenceTypeArgument = 0x4B,
}

impl TargetType {
    const ALL: [Self; 22] = [
        Self::ClassTypeParameter,
        Self::MethodTypeParameter,
        Self::Supertype,
        Self::ClassTypeParameterBound,
        Self::MethodTypeParameterBound,
        Self::Field,
        Self::MethodReturn,
        Self::Receiver,
        Self::FormalParameter,
        Self::Throws,
        Self::LocalVariable,
        Self::ResourceVariable,
        Self::ExceptionParameter,
        Self::Instanceof,
        Self::New,
        Self::ConstructorReference,
        Self::MethodReference,
        Self::Cast,
        Self::ConstructorInvocationTypeArgument,
        Self::MethodInvocationTypeArgument,
        Self::ConstructorReferenceTypeArgument,
        Self::MethodReferenceTypeArgument,
    ];

    /// The size of the fixed part of `target_info`. The fixed part of a local variable target is
    /// the u2 length of the table that follows it.
    fn info_size(self) -> usize {
        match self {
            Self::Field | Self::MethodReturn | Self::Receiver => 0,
            Self::ClassTypeParameter | Self::MethodTypeParameter | Self::FormalParameter => 1,
            Self::Supertype
            | Self::ClassTypeParameterBound
            | Self::MethodTypeParameterBound
            | Self::Throws
            | Self::LocalVariable
            | Self::ResourceVariable
            | Self::ExceptionParameter
            | Self::Instanceof
            | Self::New
            | Self::ConstructorReference
            | Self::MethodReference => 2,
            Self::Cast
            | Self::ConstructorInvocationTypeArgument
            | Self::MethodInvocationTypeArgument
            | Self::ConstructorReferenceTypeArgument
            | Self::MethodReferenceTypeArgument => 3,
        }
    }

    /// The size of one entry of the table that follows the fixed part, for the targets that
    /// have one.
    fn table_entry_size(self) -> Option<usize> {
        matches!(self, Self::LocalVariable | Self::ResourceVariable).then_some(6)
    }
}

impl TryFrom<u8> for TargetType {
    type Error = ClassError;

    fn try_from(tag: u8) -> Result<Self, ClassError> {
        Self::ALL
            .into_iter()
            .find(|known| *known as u8 == tag)
            .ok_or(ClassError::BadTargetType(tag))
    }
}

/// How the class file reads a UTF8 entry. This sets how the entry is mapped.
#[derive(Clone, Copy)]
pub(super) enum Usage {
    /// The name of a `CONSTANT_Class`: an internal name or an array descriptor.
    ClassName,
    Descriptor,
    Signature,
    /// The text of a string constant or of a string in an annotation.
    Value,
    /// The simple name of the inner class read from this `CONSTANT_Class` in `InnerClasses`.
    InnerName {
        inner_class: u16,
    },
    /// A name that jarjar never maps, such as the name of a method or of an attribute.
    Plain,
}

/// One u2 field of the class file that contains the index of a UTF8 entry.
pub(super) struct Site {
    pub offset: usize,
    pub utf8: u16,
    pub usage: Usage,
}

#[derive(Clone, Copy)]
pub(super) enum Constant {
    Utf8 { start: usize, end: usize },
    Class { name: u16 },
    Other,
}

/// Where an attribute table is. Only the annotations of a declaration are recorded.
#[derive(Clone, Copy, PartialEq)]
enum Scope {
    /// The attributes of the class, of a field or of a method.
    Declaration,
    /// The attributes of a Code attribute or of a record component.
    Nested,
}

/// One annotation of an annotation attribute.
pub(super) struct Annotation {
    /// The UTF8 entry with the descriptor of the annotation type.
    pub type_index: u16,
    pub start: usize,
    pub end: usize,
}

/// A `RuntimeVisibleAnnotations` or `RuntimeInvisibleAnnotations` attribute of a declaration.
pub(super) struct AnnotationTable {
    pub start: usize,
    pub end: usize,
    /// The offset of the `attributes_count` of the declaration.
    pub count_offset: usize,
    pub annotations: Vec<Annotation>,
}

struct Reader<'a> {
    data: &'a [u8],
    pos: usize,
}

impl<'a> Reader<'a> {
    fn read_bytes<const N: usize>(&mut self) -> Result<[u8; N], ClassError> {
        let bytes = self
            .data
            .get(self.pos..self.pos + N)
            .and_then(|bytes| bytes.try_into().ok())
            .ok_or(ClassError::Truncated)?;
        self.pos += N;
        Ok(bytes)
    }

    fn read_u1(&mut self) -> Result<u8, ClassError> {
        self.read_bytes::<1>().map(|[byte]| byte)
    }

    fn read_u2(&mut self) -> Result<u16, ClassError> {
        self.read_bytes().map(u16::from_be_bytes)
    }

    /// Reads a u2 field as a count of items.
    fn read_count(&mut self) -> Result<usize, ClassError> {
        self.read_u2().map(usize::from)
    }

    /// Reads a u4 field as a length in bytes. A length that does not fit into memory is longer
    /// than the class file.
    fn read_length(&mut self) -> Result<usize, ClassError> {
        let length = self.read_bytes().map(u32::from_be_bytes)?;
        usize::try_from(length).map_err(|_| ClassError::Truncated)
    }

    /// Reads the next `count` bytes.
    fn take(&mut self, count: usize) -> Result<&'a [u8], ClassError> {
        let bytes = self.data.get(self.pos..self.pos + count).ok_or(ClassError::Truncated)?;
        self.pos += count;
        Ok(bytes)
    }

    fn skip(&mut self, count: usize) -> Result<(), ClassError> {
        self.take(count).map(|_skipped| ())
    }

    /// Reads the u2 field at the position, without moving past it.
    fn peek_u2(&self) -> Result<u16, ClassError> {
        Reader { data: self.data, pos: self.pos }.read_u2()
    }
}

/// The parsed class file.
pub(super) struct ClassFile {
    pub pool: Vec<Constant>,
    /// The offset of `constant_pool_count`.
    pub pool_count_offset: usize,
    pub pool_end: usize,
    /// The text of each UTF8 entry, in the order of the pool.
    pub texts: BTreeMap<u16, String>,
    pub sites: Vec<Site>,
    /// The annotation attributes of the class, of its fields and of its methods.
    pub annotation_tables: Vec<AnnotationTable>,
    /// The `CONSTANT_Class` entry of the class itself.
    pub this_class: u16,
}

impl ClassFile {
    pub fn utf8_text(&self, index: u16) -> Result<&str, ClassError> {
        self.texts.get(&index).map(String::as_str).ok_or(ClassError::BadConstant { index })
    }

    /// Whether the entry is a UTF8 entry whose text has no Rust form.
    pub fn is_undecodable_utf8(&self, index: u16) -> bool {
        matches!(self.pool.get(usize::from(index)), Some(Constant::Utf8 { .. }))
            && !self.texts.contains_key(&index)
    }

    pub fn class_name(&self, index: u16) -> Result<&str, ClassError> {
        match self.pool.get(usize::from(index)) {
            Some(Constant::Class { name }) => self.utf8_text(*name),
            _ => Err(ClassError::BadConstant { index }),
        }
    }

    fn record_site(&mut self, reader: &mut Reader, usage: Usage) -> Result<(), ClassError> {
        let offset = reader.pos;
        let utf8 = reader.read_u2()?;
        if utf8 != 0 {
            self.sites.push(Site { offset, utf8, usage });
        }
        Ok(())
    }

    pub fn parse(data: &[u8]) -> Result<Self, ClassError> {
        let mut reader = Reader { data, pos: 0 };
        if reader.read_bytes()? != [0xCA, 0xFE, 0xBA, 0xBE] {
            return Err(ClassError::NotAClass);
        }
        reader.skip(4)?;
        let pool_count_offset = reader.pos;
        let count = reader.read_u2()?;
        let mut class = ClassFile {
            pool: vec![Constant::Other; usize::from(count)],
            pool_count_offset,
            pool_end: 0,
            texts: BTreeMap::new(),
            sites: Vec::new(),
            annotation_tables: Vec::new(),
            this_class: 0,
        };
        // The texts in the order of the pool.
        let mut texts = Vec::new();
        let mut index = 1;
        while index < count {
            let start = reader.pos;
            let tag = ConstantTag::try_from(reader.read_u1()?)?;
            let mut fields = Reader { data, pos: reader.pos };
            reader.skip(tag.payload_size())?;
            match tag {
                ConstantTag::Utf8 => {
                    let length = fields.read_count()?;
                    let bytes = reader.take(length)?;
                    // A string with an unpaired surrogate cannot be converted to Rust text. It
                    // cannot be a class name, so the text is not changed.
                    if let Ok(text) = cesu8::from_java_cesu8(bytes) {
                        texts.push((index, text.into_owned()));
                    }
                    class.pool[usize::from(index)] = Constant::Utf8 { start, end: reader.pos };
                }
                ConstantTag::Class => {
                    let offset = fields.pos;
                    let name = fields.read_u2()?;
                    class.pool[usize::from(index)] = Constant::Class { name };
                    class.sites.push(Site { offset, utf8: name, usage: Usage::ClassName });
                }
                ConstantTag::String => {
                    let offset = fields.pos;
                    let utf8 = fields.read_u2()?;
                    class.sites.push(Site { offset, utf8, usage: Usage::Value });
                }
                ConstantTag::NameAndType => {
                    class.record_site(&mut fields, Usage::Plain)?;
                    class.record_site(&mut fields, Usage::Descriptor)?;
                }
                ConstantTag::MethodType => class.record_site(&mut fields, Usage::Descriptor)?,
                _ => {}
            }
            index += tag.slots();
        }
        class.pool_end = reader.pos;
        class.texts = texts.into_iter().collect();

        reader.skip(2)?;
        class.this_class = reader.read_u2()?;
        reader.skip(2)?;
        let interfaces = reader.read_count()?;
        reader.skip(2 * interfaces)?;
        for _fields_then_methods in 0..2 {
            for _ in 0..reader.read_u2()? {
                reader.skip(2)?;
                class.record_site(&mut reader, Usage::Plain)?;
                class.record_site(&mut reader, Usage::Descriptor)?;
                class.read_attributes(&mut reader, Scope::Declaration)?;
            }
        }
        class.read_attributes(&mut reader, Scope::Declaration)?;
        Ok(class)
    }

    fn read_attributes(&mut self, reader: &mut Reader, scope: Scope) -> Result<(), ClassError> {
        let count_offset = reader.pos;
        for _ in 0..reader.read_u2()? {
            let start = reader.pos;
            let name = self.utf8_text(reader.peek_u2()?)?.to_owned();
            self.record_site(reader, Usage::Plain)?;
            let length = reader.read_length()?;
            let end = reader.pos + length;
            if end > reader.data.len() {
                return Err(ClassError::Truncated);
            }
            match name.as_str() {
                "Signature" => self.record_site(reader, Usage::Signature)?,
                "SourceFile" => self.record_site(reader, Usage::Plain)?,
                "MethodParameters" => {
                    for _ in 0..reader.read_u1()? {
                        self.record_site(reader, Usage::Plain)?;
                        reader.skip(2)?;
                    }
                }
                "Code" => {
                    reader.skip(4)?;
                    let code_length = reader.read_length()?;
                    reader.skip(code_length)?;
                    let handlers = reader.read_count()?;
                    reader.skip(8 * handlers)?;
                    self.read_attributes(reader, Scope::Nested)?;
                }
                "LocalVariableTable" => self.read_local_variables(reader, Usage::Descriptor)?,
                "LocalVariableTypeTable" => self.read_local_variables(reader, Usage::Signature)?,
                "InnerClasses" => {
                    for _ in 0..reader.read_u2()? {
                        let inner_class = reader.read_u2()?;
                        reader.skip(2)?;
                        self.record_site(reader, Usage::InnerName { inner_class })?;
                        reader.skip(2)?;
                    }
                }
                "RuntimeVisibleAnnotations" | "RuntimeInvisibleAnnotations" => {
                    let mut annotations = Vec::new();
                    for _ in 0..reader.read_u2()? {
                        let annotation_start = reader.pos;
                        let type_index = reader.peek_u2()?;
                        self.read_annotation(reader)?;
                        annotations.push(Annotation {
                            type_index,
                            start: annotation_start,
                            end: reader.pos,
                        });
                    }
                    if scope == Scope::Declaration {
                        self.annotation_tables.push(AnnotationTable {
                            start,
                            end,
                            count_offset,
                            annotations,
                        });
                    }
                }
                "RuntimeVisibleParameterAnnotations" | "RuntimeInvisibleParameterAnnotations" => {
                    for _ in 0..reader.read_u1()? {
                        for _ in 0..reader.read_u2()? {
                            self.read_annotation(reader)?;
                        }
                    }
                }
                "RuntimeVisibleTypeAnnotations" | "RuntimeInvisibleTypeAnnotations" => {
                    for _ in 0..reader.read_u2()? {
                        Self::skip_type_annotation_target(reader)?;
                        let path_length = usize::from(reader.read_u1()?);
                        reader.skip(2 * path_length)?;
                        self.read_annotation(reader)?;
                    }
                }
                "AnnotationDefault" => self.read_element_value(reader)?,
                "Record" => {
                    for _ in 0..reader.read_u2()? {
                        self.record_site(reader, Usage::Plain)?;
                        self.record_site(reader, Usage::Descriptor)?;
                        self.read_attributes(reader, Scope::Nested)?;
                    }
                }
                _ => {}
            }
            reader.pos = end;
        }
        Ok(())
    }

    /// Reads a `LocalVariableTable` or `LocalVariableTypeTable`, whose entries read their type
    /// with `usage`.
    fn read_local_variables(
        &mut self,
        reader: &mut Reader,
        usage: Usage,
    ) -> Result<(), ClassError> {
        for _ in 0..reader.read_u2()? {
            reader.skip(4)?;
            self.record_site(reader, Usage::Plain)?;
            self.record_site(reader, usage)?;
            reader.skip(2)?;
        }
        Ok(())
    }

    fn read_annotation(&mut self, reader: &mut Reader) -> Result<(), ClassError> {
        self.record_site(reader, Usage::Descriptor)?;
        for _ in 0..reader.read_u2()? {
            self.record_site(reader, Usage::Plain)?;
            self.read_element_value(reader)?;
        }
        Ok(())
    }

    fn read_element_value(&mut self, reader: &mut Reader) -> Result<(), ClassError> {
        match reader.read_u1()? {
            b'B' | b'C' | b'D' | b'F' | b'I' | b'J' | b'S' | b'Z' => reader.skip(2),
            b's' => self.record_site(reader, Usage::Value),
            b'e' => {
                self.record_site(reader, Usage::Descriptor)?;
                reader.skip(2)
            }
            b'c' => self.record_site(reader, Usage::Descriptor),
            b'@' => self.read_annotation(reader),
            b'[' => {
                for _ in 0..reader.read_u2()? {
                    self.read_element_value(reader)?;
                }
                Ok(())
            }
            tag => Err(ClassError::BadElementValue(tag)),
        }
    }

    /// Skips the `target_info` of a type annotation.
    fn skip_type_annotation_target(reader: &mut Reader) -> Result<(), ClassError> {
        let target = TargetType::try_from(reader.read_u1()?)?;
        let table = match target.table_entry_size() {
            Some(entry_size) => entry_size * usize::from(reader.peek_u2()?),
            None => 0,
        };
        reader.skip(target.info_size() + table)
    }
}
